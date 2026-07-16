package executor

// TestProbe_BUG001_InsertWithoutColumnList probes BUG-001: parseInsert()
// calls expect(LPAREN) unconditionally at parser.go:1195 before scanning
// column names. This makes INSERT INTO t VALUES (...) (without a column list)
// a parse error because the parser expects an opening parenthesis for column
// names but instead finds the VALUES keyword.
//
// Repro SQL: INSERT INTO customers VALUES (101, 'Alice Test', 'alice@example.com', 'US', '2024-01-01')
// Expected: parse succeeds and the row is inserted
// Actual:   parse error — "expected column name, got VALUES"

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProbe_BUG001_InsertWithoutColumnList(t *testing.T) {
	db := newTestDB(t)

	// Verify the table exists and has data before the probe insert.
	before := db.run(t, "SELECT id FROM customers WHERE id = 101")
	require.Equal(t, 0, len(before.Rows), "no row with id=101 should exist before insert")

	// This form — INSERT INTO t VALUES (...) without an explicit column list —
	// must be a parse error due to the bug. db.run calls require.NoError on
	// the parse step, so it will fail here if the bug is present.
	db.run(t, "INSERT INTO customers VALUES (101, 'Alice Test', 'alice@example.com', 'US', '2024-01-01')")

	// Confirm the row landed.
	after := db.run(t, "SELECT id FROM customers WHERE id = 101")
	require.Equal(t, 1, len(after.Rows), "inserted row should be retrievable")
}
