package executor

import (
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG005_WindowFuncInWhere probes BUG-005 (window-function-in-WHERE):
//
// The semantic analyzer (analyzer/analyzer.go ~line 199) checks
// containsAggregate(sel.Where) to forbid aggregate functions in WHERE, but has
// no equivalent containsWindowFunc check.  A WHERE clause containing a window
// function expression — e.g. WHERE RANK() OVER (ORDER BY amount) < 3 — passes
// analysis without error.
//
// At execution time the expression evaluator's fallback case
// (expression.go lines 104-107) calls the inner function as a plain scalar via
// evalFunction(e.Func, ...). RANK is not implemented as a scalar function, so
// the engine returns a runtime error "unknown function: RANK" instead of the
// expected plan-time analysis error.
//
// Expected: analysis returns an error such as
//   "window functions are not allowed in WHERE clause"
//
// Actual (with bug): analysis succeeds (no error), and execution later fails
// with a runtime error "unknown function: RANK", producing silent wrong results
// for any window function that happens to be implemented as a scalar.
func TestProbe_BUG005_WindowFuncInWhere(t *testing.T) {
	db := newTestDB(t)

	const sql = `SELECT id, amount FROM orders WHERE RANK() OVER (ORDER BY amount) < 3`

	// ── Step 1: Verify that the analyzer does NOT reject the query ────────────
	// This is the core of the bug: there is no containsWindowFunc guard in the
	// WHERE clause analysis path.
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse should succeed")

	a := analyzer.New(db.cat)
	analyzeErr := a.Analyze(stmt)

	if analyzeErr != nil {
		// Bug is fixed: analysis correctly rejected the query.
		msg := strings.ToLower(analyzeErr.Error())
		if strings.Contains(msg, "window") || strings.Contains(msg, "where") ||
			strings.Contains(msg, "not allowed") {
			t.Logf("BUG-005 NOT present (fixed): analysis correctly rejected window func in WHERE: %v", analyzeErr)
		} else {
			t.Logf("Analysis returned an unexpected error (not the window-func guard): %v", analyzeErr)
		}
		return
	}

	// Analysis passed — bug is present.
	t.Logf("BUG-005 CONFIRMED: analyzer accepted RANK() OVER (...) in WHERE clause without error")

	// ── Step 2: Run end-to-end to observe the runtime failure ────────────────
	// With the bug, execution reaches the expression evaluator which hits the
	// fallback case (expression.go line 107: evalFunction(e.Func, tuple, ctx))
	// and returns "unknown function: RANK" at runtime.
	result, execErr := runAllowError(t, db, sql)
	if execErr != nil {
		t.Logf("Execution error (runtime, not analysis): %v", execErr)
	} else {
		t.Logf("Execution produced %d row(s) (silent wrong result):", len(result.Rows))
		for i, row := range result.Rows {
			t.Logf("  row[%d]: %v", i, row)
		}
	}

	// The bug means analysis returned nil when it should have returned an error.
	assert.Error(t, analyzeErr,
		"BUG-005: analyzer should reject RANK() OVER (...) in WHERE clause with "+
			"'window functions are not allowed in WHERE clause', but returned nil")
}
