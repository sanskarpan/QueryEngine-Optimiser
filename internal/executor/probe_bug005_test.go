package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG005_FullJoinEvalErrorSwallowed probes BUG-005:
// In NestedLoopJoin.nextRightJoin (nl_join.go lines 222-226), an error returned
// by EvalExpr during the condition check is silently swallowed with `continue`
// instead of being propagated to the caller.
//
// For a FULL OUTER JOIN where the ON condition always causes an eval error (e.g.
// adding an integer column to the string literal 'x'), every left row causes
// an error for every right row. Because the error is swallowed, rightMatched[i]
// stays false for every right row. When phase 2 runs it then emits a
// null-padded row for each right row — producing spurious output instead of
// returning a runtime error.
//
// Expected: Execute() returns a non-nil error.
// Actual (with bug): Execute() returns nil error and emits spurious null-padded rows.
func TestProbe_BUG005_FullJoinEvalErrorSwallowed(t *testing.T) {
	// Build a fresh in-memory DB with two tiny tables so we control data precisely.
	// We use the catalog/storage API directly because the logical planner does not
	// support DDL (CREATE TABLE) statements.
	cat := catalog.New()
	store := storage.New()

	// Register table a(id INT) in catalog and storage.
	require.NoError(t, store.CreateTable("a"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "a",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
		},
	}))

	// Register table b(id INT) in catalog and storage.
	require.NoError(t, store.CreateTable("b"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "b",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
		},
	}))

	db := &testDB{cat: cat, store: store}

	// Insert one row into each table.
	db.run(t, `INSERT INTO a (id) VALUES (1)`)
	db.run(t, `INSERT INTO b (id) VALUES (1)`)

	// The ON condition `a.id + 'x' = b.id` is a type error at runtime:
	// adding an integer to the string 'x' should produce an EvalExpr error.
	// With BUG-005 present, the error is swallowed in nextRightJoin and the
	// engine returns rows instead of an error.
	result, err := runAllowError(t, db,
		`SELECT a.id, b.id FROM a FULL OUTER JOIN b ON a.id + 'x' = b.id`)

	if err != nil {
		// Correct behaviour: a runtime error was propagated.
		t.Logf("BUG-005 NOT present (or fixed): error correctly propagated: %v", err)
		return
	}

	// If we reach here, the bug is present: no error was returned.
	// Expose the spurious output for diagnosis.
	require.NotNil(t, result, "result should not be nil when no error is returned")
	t.Logf("BUG-005 CONFIRMED: Execute() returned nil error; got %d row(s):", len(result.Rows))
	for i, row := range result.Rows {
		t.Logf("  row[%d]: %v", i, row)
	}

	// Assert that an error SHOULD have been returned (this will fail, confirming the bug).
	assert.NotNil(t, err,
		"BUG-005: EvalExpr error in nextRightJoin was silently swallowed; "+
			"Execute() should have returned a runtime error but returned nil")
}
