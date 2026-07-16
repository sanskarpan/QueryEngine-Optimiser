package executor

// TestProbe_NLJBug001_LeftJoinSwallowsEvalError probes BUG-001:
// NestedLoopJoin does `continue` instead of `return nil, err` when
// EvalExpr returns an error during condition evaluation (nl_join.go).
//
// For LEFT JOIN this produces a spurious null-padded row: every right-side
// row causes an error, so `leftMatched` stays false; after exhausting all
// right rows the operator incorrectly emits a NULL-extended left row as if
// no right rows matched — even though right rows do exist.
//
// Expected behaviour: the runtime error is propagated to the caller.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_NLJBug001_LeftJoinSwallowsEvalError(t *testing.T) {
	db := newTestDB(t)

	// The join condition `c.id + 'notanumber' = o.customer_id` adds an integer
	// column to a string literal. Value.Add() returns a type-mismatch error for
	// INT + TEXT.  Because both tables have rows, EvalExpr will return an error
	// on every right-side candidate.  The bug causes the NLJ to swallow every
	// such error (via `continue`) and ultimately emit a null-padded left row for
	// each customer instead of propagating the error to the caller.
	sql := `SELECT c.id, o.id FROM customers c LEFT JOIN orders o ON c.id + 'notanumber' = o.customer_id LIMIT 5`

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse should succeed")

	a := analyzer.New(db.cat)
	require.NoError(t, a.Analyze(stmt), "analyze should succeed")

	lb := logical.NewBuilder(db.cat)
	lplan, err := lb.BuildStatement(stmt)
	require.NoError(t, err, "logical build should succeed")

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	require.NoError(t, err, "physical build should succeed")

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = lb.GetCTEs()

	result, execErr := Execute(pplan, ctx)

	if execErr != nil {
		// Correct behaviour: the type-mismatch error from EvalExpr was propagated.
		t.Logf("Execute returned error (expected): %v", execErr)
		assert.Contains(t, execErr.Error(), "type mismatch",
			"error should mention type mismatch")
	} else {
		// Bug is present: no error was raised.
		require.NotNil(t, result)
		t.Logf("BUG CONFIRMED: Execute returned %d rows instead of an error", len(result.Rows))
		t.Logf("First few rows (should not exist):")
		for i, row := range result.Rows {
			if i >= 3 {
				break
			}
			t.Logf("  row %d: %v", i, row)
		}
		// Verify the bug signature: every right-side column is NULL even though
		// orders table has matching customer rows.
		for i, row := range result.Rows {
			if i >= 5 {
				break
			}
			if len(row) >= 2 {
				assert.True(t, row[1].IsNull,
					"row %d: o.id should be NULL (spurious null-padded row from swallowed error)", i)
			}
		}
		assert.Fail(t, "expected a runtime error for INT + TEXT in join condition, got a result set",
			"got %d rows with null-padded right columns — BUG-001 confirmed", len(result.Rows))
	}
}
