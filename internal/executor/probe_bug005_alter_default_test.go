package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG005_AlterTableAddColumnDefault probes BUG-005 (ALTER TABLE area):
// ALTER TABLE ADD COLUMN with a DEFAULT value is silently ignored.
//
// Three layers of defect are present:
//  1. ast.ColumnDef has no Default field and catalog.Column has no Default field,
//     so no default expression is ever stored.
//  2. parseColumnDef() does not recognise the DEFAULT keyword; it stops parsing
//     constraints when it sees DEFAULT, leaving the token unconsumed so the
//     parser rejects the statement.
//  3. AddColumnNulls() unconditionally appends NULL to every existing row,
//     regardless of any DEFAULT value; existing rows should receive the default.
//  4. InsertOp initialises all unmentioned columns to NullValue() and has no
//     mechanism to apply stored defaults for newly inserted rows.
//
// Repro SQL (conceptual):
//
//	ALTER TABLE t ADD COLUMN status TEXT DEFAULT 'active';
//	SELECT status FROM t;
//
// Expected: the single pre-existing row has status = 'active'
// Actual (with bug): the ALTER either fails to parse entirely, or the row
// has status = NULL because AddColumnNulls() writes NULL unconditionally.
func TestProbe_BUG005_AlterTableAddColumnDefault(t *testing.T) {
	// Build an in-memory DB directly (CREATE TABLE is not supported by the
	// logical planner, so we use the catalog/storage API directly).
	cat := catalog.New()
	store := storage.New()

	// Create table t(id INT).
	require.NoError(t, store.CreateTable("t"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "t",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
		},
	}))

	db := &testDB{cat: cat, store: store}

	// Insert a row before the column is added.
	db.run(t, `INSERT INTO t (id) VALUES (1)`)

	// Attempt ALTER TABLE t ADD COLUMN status TEXT DEFAULT 'active'.
	// runAllowError is used because the parser may reject the statement
	// (parseColumnDef does not handle DEFAULT), which is itself a manifestation
	// of the bug.
	alterResult, alterErr := runAllowError(t, db,
		`ALTER TABLE t ADD COLUMN status TEXT DEFAULT 'active'`)

	if alterErr != nil {
		// Parse or execution failed — the DEFAULT clause is not supported.
		t.Logf("BUG-005 (ALTER TABLE DEFAULT) CONFIRMED at ALTER step: %v", alterErr)
		// Assert no error to make the test fail visibly and report the bug.
		require.NoError(t, alterErr,
			"BUG-005: ALTER TABLE ADD COLUMN with DEFAULT should succeed; "+
				"parseColumnDef() does not parse the DEFAULT clause")
		return
	}
	require.NotNil(t, alterResult)

	// If the ALTER succeeded (e.g. DEFAULT was silently ignored and the column
	// was added without a default), verify the value in the pre-existing row.
	result := db.run(t, `SELECT status FROM t WHERE id = 1`)
	require.Len(t, result.Rows, 1, "expected exactly one row in t")
	require.Len(t, result.Rows[0], 1, "expected exactly one column (status)")

	statusVal := result.Rows[0][0]
	t.Logf("BUG-005: status value for pre-existing row: isNull=%v strVal=%q",
		statusVal.IsNull, statusVal.StrVal)

	// With the bug, AddColumnNulls() writes NULL; the correct value is 'active'.
	assert.False(t, statusVal.IsNull,
		"BUG-005: pre-existing row status should be 'active' (the DEFAULT), but got NULL; "+
			"AddColumnNulls() ignores the DEFAULT value")
	assert.Equal(t, "active", statusVal.StrVal,
		"BUG-005: pre-existing row status should equal the DEFAULT value 'active'")
}
