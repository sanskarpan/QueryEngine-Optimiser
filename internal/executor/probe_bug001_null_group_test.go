package executor

// Probe test for BUG-001: NULL grouping collision in HashAggregate.
//
// In hash_agg.go the group key is built via v.String() (line 185), which returns
// the string literal "NULL" for any null Value. This means a row with a NULL column
// and a row with the text value "NULL" both produce the key "NULL" and are merged
// into the same group. SQL mandates these form distinct groups.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
)

// setupNullTable registers a table named "t_bug001" with columns (a TEXT, b INT)
// directly in the catalog and storage, bypassing the SQL DDL path which is not
// supported by the logical planner.
func setupNullTable(t *testing.T, db *testDB) {
	t.Helper()

	tbl := &catalog.Table{
		Name: "t_bug001",
		Columns: []catalog.Column{
			{Name: "a", Type: catalog.TypeText, Nullable: true, Index: 0},
			{Name: "b", Type: catalog.TypeInt, Nullable: true, Index: 1},
		},
	}
	if err := db.cat.Register(tbl); err != nil {
		t.Fatalf("register table: %v", err)
	}
	if err := db.store.CreateTable("t_bug001"); err != nil {
		t.Fatalf("create storage table: %v", err)
	}
}

// seedNullRows inserts the three rows via SQL INSERT (which goes through the
// full executor pipeline including the InsertOp).
func seedNullRows(t *testing.T, db *testDB) {
	t.Helper()
	// Row 1: a = NULL, b = 1
	db.run(t, `INSERT INTO t_bug001 (a, b) VALUES (NULL, 1)`)
	// Row 2: a = NULL, b = 2
	db.run(t, `INSERT INTO t_bug001 (a, b) VALUES (NULL, 2)`)
	// Row 3: a = 'NULL' (the text string four characters), b = 3
	db.run(t, `INSERT INTO t_bug001 (a, b) VALUES ('NULL', 3)`)
}

func TestProbe_BUG001_NullGroupByCollision(t *testing.T) {
	db := newTestDB(t)
	setupNullTable(t, db)
	seedNullRows(t, db)

	result := db.run(t, `SELECT a, COUNT(*) FROM t_bug001 GROUP BY a`)

	// SQL requires TWO distinct groups:
	//   group 1: a IS NULL   -> COUNT(*) = 2
	//   group 2: a = 'NULL'  -> COUNT(*) = 1
	//
	// With the bug, Value.String() returns "NULL" for both the actual SQL NULL
	// and the text string 'NULL', so the hash map collapses them into ONE group
	// with COUNT(*) = 3.

	t.Logf("actual row count: %d", len(result.Rows))
	for i, row := range result.Rows {
		a := row[0]
		cnt := row[1]
		var aStr string
		if a.IsNull {
			aStr = "<NULL>"
		} else {
			aStr = "'" + a.StrVal + "'"
		}
		t.Logf("  row[%d]: a=%s, count=%d", i, aStr, cnt.IntVal)
	}

	if len(result.Rows) != 2 {
		t.Errorf("BUG-001 CONFIRMED: expected 2 groups (NULL and 'NULL'), got %d group(s)", len(result.Rows))
		return
	}

	// Verify the two groups have the correct counts.
	nullCount := int64(0)
	textNullCount := int64(0)
	for _, row := range result.Rows {
		a := row[0]
		cnt := row[1]
		if a.IsNull {
			nullCount = cnt.IntVal
		} else if a.Type == catalog.TypeText && a.StrVal == "NULL" {
			textNullCount = cnt.IntVal
		} else {
			t.Errorf("unexpected group value: IsNull=%v Type=%v StrVal=%q", a.IsNull, a.Type, a.StrVal)
		}
	}

	if nullCount != 2 {
		t.Errorf("expected NULL group to have COUNT=2, got %d", nullCount)
	}
	if textNullCount != 1 {
		t.Errorf("expected 'NULL' text group to have COUNT=1, got %d", textNullCount)
	}
}

// Ensure the storage package is used (for the CreateTable call above).
var _ = storage.New
