package executor

// TestProbe_B08_MulOverflowUnderflow probes bug B08:
// The multiplication overflow guard in types.go Mul() is missing the case
// where a > 0 && b < 0 && a*b underflows past MinInt64.
// SELECT 9223372036854775807 * -2 should return an integer overflow error
// but instead silently wraps to 2.

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

func TestProbe_B08_MulOverflowUnderflow(t *testing.T) {
	db := newTestDB(t)

	sql := "SELECT 9223372036854775807 * -2"

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

	// The multiplication 9223372036854775807 * -2 underflows int64 (result would be
	// -18446744073709551614, which wraps to 2 in two's-complement arithmetic).
	// The guard should have caught this and returned an overflow error.
	if execErr != nil {
		// Correct behaviour: an overflow error was raised.
		t.Logf("Execute returned error (expected): %v", execErr)
		assert.Contains(t, execErr.Error(), "overflow", "error should mention overflow")
	} else {
		// Bug present: no error was raised.
		require.NotNil(t, result, "result must not be nil when no error")
		require.NotEmpty(t, result.Rows, "result must have rows")
		actual := result.Rows[0][0]
		t.Logf("BUG CONFIRMED: SELECT 9223372036854775807 * -2 returned %v instead of an overflow error", actual)
		// Fail the test explicitly to surface the bug.
		assert.Fail(t, "expected integer overflow error but got a result",
			"actual value returned: %v (silently wrapped)", actual)
	}
}
