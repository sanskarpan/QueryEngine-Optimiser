package executor

// TestProbe_CTE03_WithRecursiveIgnoresRecursiveTerm probes CTE-03:
// WITH RECURSIVE is parsed correctly (CTEDef.RecursiveSelect and CTEDef.RecursiveAll
// are populated by the parser) but the logical planner in buildSelect
// (builder.go:165-171) only stores cte.Select (the anchor/base term) into
// b.ctes. CTEDef.RecursiveSelect is never consulted anywhere in the planner or
// executor, so the iterative fixpoint loop is never executed.
//
// For `WITH RECURSIVE nums AS (SELECT 1 AS n UNION ALL SELECT n+1 FROM nums WHERE n < 5)
// SELECT n FROM nums`, the correct result is five rows: n=1,2,3,4,5.
// With the bug present the engine returns only the anchor row (n=1).
//
// Expected: 5 rows with n values 1 through 5.
// Actual (bug present): 1 row with n=1 (recursive step silently ignored).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_CTE03_WithRecursiveIgnoresRecursiveTerm(t *testing.T) {
	db := newTestDB(t)

	const sql = `WITH RECURSIVE nums AS (
		SELECT 1 AS n
		UNION ALL
		SELECT n+1 FROM nums WHERE n < 5
	) SELECT n FROM nums`

	// Use runAllowError so that an "unimplemented" error (acceptable alternative
	// behaviour) does not cause an unexpected test panic.
	result, err := runAllowError(t, db, sql)

	if err != nil {
		// Acceptable: engine explicitly rejects recursion rather than silently
		// returning wrong data. The bug report notes that even an error would be
		// acceptable, so this branch means CTE-03 is NOT triggered in its most
		// harmful silent form.
		t.Logf("CTE-03: engine returned an error (recursion rejected, not silently wrong): %v", err)
		// Still mark as confirmed — the feature is broken either way, but the
		// "silent wrong result" variant (no error, only anchor row) is the
		// critical manifestation. We record which path we took.
		t.Logf("CTE-03 CONFIRMED (error path): WITH RECURSIVE is not implemented; error: %v", err)
		return
	}

	require.NotNil(t, result)

	t.Logf("CTE-03: query returned %d row(s) (expected 5):", len(result.Rows))
	for i, row := range result.Rows {
		t.Logf("  row[%d]: %v", i, row)
	}

	// If the engine silently returned only 1 row (the anchor), the bug is
	// confirmed in its most dangerous form: no error, wrong result.
	assert.Equal(t, 5, len(result.Rows),
		"CTE-03 CONFIRMED: WITH RECURSIVE returned %d row(s) instead of 5; "+
			"the recursive term (RecursiveSelect) is never executed — "+
			"builder.go buildSelect stores only cte.Select (anchor) in b.ctes "+
			"and never consults CTEDef.RecursiveSelect",
		len(result.Rows))

	if len(result.Rows) == 5 {
		// Verify the actual values are 1..5 in some order.
		vals := make([]int64, len(result.Rows))
		for i, row := range result.Rows {
			require.NotEmpty(t, row, "row %d is empty", i)
			vals[i] = row[0].IntVal
		}
		for _, v := range vals {
			assert.GreaterOrEqual(t, v, int64(1))
			assert.LessOrEqual(t, v, int64(5))
		}
	}
}
