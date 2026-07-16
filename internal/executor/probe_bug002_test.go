package executor

// TestProbe_BUG002_NullByteGroupKeyCollision probes BUG-002:
// The HashAggregate operator builds group keys by joining per-column value
// strings with "\x00" as separator (hash_agg.go, line 187):
//
//	keyStr := strings.Join(keyParts, "\x00")
//
// If a TEXT column value itself contains the byte 0x00, the separator is
// indistinguishable from part of the value, causing two logically distinct
// groups to map to the same keyStr and therefore be merged into one group.
//
// Example collision:
//
//	row 1: col a = "a\x00b", col b = "c"  → keyStr = "a\x00b\x00c"
//	row 2: col a = "a",      col b = "b\x00c" → keyStr = "a\x00b\x00c"
//
// Both produce the identical keyStr, so they are incorrectly merged into one
// group instead of two.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG002_NullByteGroupKeyCollision(t *testing.T) {
	// Build a fresh, isolated in-memory database (no seed data needed).
	cat := catalog.New()
	store := storage.New()

	// Register table schema: t(a TEXT, b TEXT)
	tbl := &catalog.Table{
		Name: "t",
		Columns: []catalog.Column{
			{Name: "a", Type: catalog.TypeText, Index: 0},
			{Name: "b", Type: catalog.TypeText, Index: 1},
		},
	}
	require.NoError(t, cat.Register(tbl))
	require.NoError(t, store.CreateTable("t"))

	// Insert rows directly via the storage layer so we can embed real \x00 bytes
	// that the SQL lexer would not produce from a string literal.
	//
	//   row 1: a = "a\x00b",  b = "c"     → expected group key part: "a\x00b" | "c"
	//   row 2: a = "a",       b = "b\x00c" → expected group key part: "a"     | "b\x00c"
	//
	// With the buggy separator the two keyStr values are both "a\x00b\x00c",
	// causing them to collide.
	heap := store.MustGetTable("t")
	heap.Insert(storage.Tuple{Values: []catalog.Value{
		catalog.TextValue("a\x00b"),
		catalog.TextValue("c"),
	}})
	heap.Insert(storage.Tuple{Values: []catalog.Value{
		catalog.TextValue("a"),
		catalog.TextValue("b\x00c"),
	}})

	db := &testDB{cat: cat, store: store}

	// GROUP BY a, b should produce exactly 2 rows, each with COUNT(*) = 1.
	result := db.run(t, "SELECT a, b, COUNT(*) FROM t GROUP BY a, b")

	// The critical assertion: two distinct groups must be returned.
	assert.Equal(t, 2, len(result.Rows),
		"expected 2 distinct groups but got %d — null-byte separator collision merges them into 1",
		len(result.Rows))

	// If we got 2 rows, verify each group has a count of 1.
	if len(result.Rows) == 2 {
		for i, row := range result.Rows {
			require.Len(t, row, 3, "each row should have 3 columns (a, b, COUNT(*))")
			count := row[2].IntVal
			assert.Equal(t, int64(1), count,
				"row %d: expected COUNT(*)=1 but got %d", i, count)
		}
	}
}
