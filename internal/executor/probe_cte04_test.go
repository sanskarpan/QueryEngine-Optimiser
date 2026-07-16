package executor

// TestProbe_CTE04_NonRecursiveUnionDropped probes CTE-04:
// In parseWithClause (parser.go:1385), the UNION check fires unconditionally —
// even when the CTE was NOT declared WITH RECURSIVE. For a non-recursive CTE
// whose body is a set operation such as:
//
//	WITH t AS (SELECT 1 AS n UNION SELECT 2 AS n) SELECT n FROM t
//
// the parser captures only "SELECT 1 AS n" into baseSel / cte.Select, and
// "SELECT 2 AS n" is stored in cte.RecursiveSelect. Because the planner only
// registers cte.Select (builder.go:170), the second branch is completely
// dropped and the CTE returns one row instead of two.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_CTE04_NonRecursiveUnionDropped(t *testing.T) {
	db := newTestDB(t)

	const sql = `WITH t AS (SELECT 1 AS n UNION SELECT 2 AS n) SELECT n FROM t`

	// Use runAllowError so that a parse/plan/exec error is captured rather than
	// failing the test immediately — the query must be valid SQL and should not
	// error at all.
	result, err := runAllowError(t, db, sql)

	if err != nil {
		// A hard error is an unexpected failure for this valid SQL.
		t.Logf("CTE-04: engine returned an error for valid SQL: %v", err)
		t.Logf("CTE-04 CONFIRMED (error path): non-recursive CTE with UNION body " +
			"could not be executed; parser.go:1385 UNION check fires unconditionally " +
			"and the second SELECT is misrouted into RecursiveSelect, " +
			"causing a downstream failure.")
		// Fail with a descriptive message — a valid SQL must not error.
		require.NoError(t, err,
			"CTE-04: non-recursive UNION CTE should not produce an error")
		return
	}

	require.NotNil(t, result)

	t.Logf("CTE-04: query returned %d row(s) (expected 2):", len(result.Rows))
	for i, row := range result.Rows {
		t.Logf("  row[%d]: %v", i, row)
	}

	// The correct result is two rows: n=1 and n=2.
	// With the bug, only n=1 is returned (the UNION second branch is silently dropped).
	assert.Equal(t, 2, len(result.Rows),
		"CTE-04 BUG: non-recursive CTE WITH UNION body returned %d row(s) instead "+
			"of 2; parser.go:1385 fires the UNION split for ANY CTE, not only "+
			"WITH RECURSIVE ones — 'SELECT 2 AS n' is stored in RecursiveSelect "+
			"and never used by the planner (builder.go:170 only stores cte.Select)",
		len(result.Rows))

	if len(result.Rows) == 1 {
		t.Logf("CTE-04 CONFIRMED: got 1 row (n=%v), expected 2 rows (n=1, n=2); "+
			"the UNION second branch was silently dropped.", result.Rows[0])
	}
}
