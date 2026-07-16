package executor

// TestProbe_CTE01_DoubleExecution probes CTE-01: CTEs are re-executed on every
// reference — no materialization.
//
// In buildTableRef (internal/planner/logical/builder.go:318-329), each time a
// CTE name appears as a table reference, buildSelect(cteSel) is called producing
// a fresh, independent plan sub-tree.  A query like:
//
//	WITH t AS (...) SELECT ... FROM t a JOIN t b ON ...
//
// builds two separate SeqScan+Filter trees for t — one for the "a" alias, one for
// the "b" alias.  The CTE body is executed twice (or N times for N references).
//
// The bug is a correctness-and-efficiency bug.  For a purely read-only CTE over a
// static table it still produces the correct answer (both copies see the same
// data), but it violates SQL semantics for side-effecting or non-deterministic
// CTEs, and it is wasteful.
//
// To surface the observable correctness dimension we use a self-join on the CTE:
//
//	WITH t AS (SELECT id FROM customers WHERE id <= 50)
//	SELECT a.id, b.id FROM t a JOIN t b ON a.id = b.id
//
// Expected: 50 rows (one for each id 1..50 matching itself).
// Because both halves of the join scan the same base table through independent
// plan trees the result count must still be 50; however the test also intruments
// the logical plan to confirm that two separate LogicalSubquery nodes are built
// (the materialization bug), rather than a single shared node.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// countLogicalSubqueries performs a depth-first walk of the logical plan and
// returns the number of LogicalSubquery nodes found.
func countLogicalSubqueries(plan logical.Plan) int {
	if plan == nil {
		return 0
	}
	count := 0
	if _, ok := plan.(*logical.LogicalSubquery); ok {
		count++
	}
	for _, child := range plan.Children() {
		count += countLogicalSubqueries(child)
	}
	return count
}

func TestProbe_CTE01_DoubleExecution(t *testing.T) {
	db := newTestDB(t)

	reproSQL := `WITH t AS (SELECT id FROM customers WHERE id <= 50)
SELECT a.id, b.id FROM t a JOIN t b ON a.id = b.id`

	// ── Step 1: run the query end-to-end and verify the result count ──────────
	result := db.run(t, reproSQL)

	// The self-join on id should yield exactly 50 rows (id 1..50 × themselves).
	assert.Equal(t, 50, len(result.Rows),
		"CTE self-join should return 50 rows (one per id in 1..50)")

	// ── Step 2: build only the logical plan and inspect the tree ──────────────
	p := parser.New(reproSQL)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse")

	a := analyzer.New(db.cat)
	require.NoError(t, a.Analyze(stmt), "analyze")

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	require.NoError(t, err, "logical build")

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	_, err = pb.Build(lplan)
	require.NoError(t, err, "physical build")

	// Count how many LogicalSubquery nodes are in the plan for the CTE.
	// A correct implementation with shared materialisation would have 1 node.
	// The buggy implementation creates one per reference → 2 here.
	subqueryCount := countLogicalSubqueries(lplan)
	t.Logf("LogicalSubquery nodes in plan: %d", subqueryCount)

	if subqueryCount >= 2 {
		t.Logf("BUG CTE-01 CONFIRMED: found %d LogicalSubquery nodes for a CTE "+
			"referenced twice — the CTE body will be executed %d times instead of once.",
			subqueryCount, subqueryCount)
	}

	// The assertion below documents the bug: we expect the buggy engine to
	// produce 2 subquery nodes (one per CTE reference).  When the bug is fixed,
	// this count will drop to 1 (or 0 if a dedicated LogicalCTE node is used).
	assert.GreaterOrEqual(t, subqueryCount, 2,
		"CTE-01 BUG: expected ≥2 LogicalSubquery nodes (one per CTE reference) "+
			"because the engine does not materialise CTE results; "+
			"got %d — if this is now 1 the bug may have been fixed", subqueryCount)

	// ── Step 3: verify the executor actually produces the right rows ───────────
	// Even with the bug the rows should be correct for a read-only CTE over
	// a stable table.  This confirms the engine at least runs without error.
	require.Len(t, result.Columns, 2, "should have two columns: a.id, b.id")
	for _, row := range result.Rows {
		require.Len(t, row, 2)
		assert.Equal(t, row[0].IntVal, row[1].IntVal,
			"join condition a.id = b.id must hold for every row")
	}

	// Also run the broader exec to capture the actual output for the report.
	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	t.Logf("Execution result: %d rows, columns=%v", len(result.Rows), result.Columns)
	if len(result.Rows) > 0 {
		t.Logf("First row: a.id=%v b.id=%v", result.Rows[0][0].IntVal, result.Rows[0][1].IntVal)
	}
}
