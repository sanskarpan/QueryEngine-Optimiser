package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG004_RangeModeIgnored probes BUG-004:
// resolveFrame in executor/operators/window.go reads spec.Frame.Start and
// spec.Frame.End to compute physical row offsets but never inspects
// spec.Frame.Mode ("ROWS" or "RANGE").
//
// In RANGE mode with CURRENT ROW, the frame must expand to include all peer
// rows that share the same ORDER BY value as the current row (peer-group
// semantics). Because Mode is never consulted, the engine treats RANGE as
// ROWS, so peer rows beyond the physical position of CURRENT ROW are
// incorrectly excluded from the frame.
//
// Setup: a tiny table with three rows where two rows share the same
// "category" value ('A', 'A', 'B').
//
// Query:
//
//	SELECT val, SUM(val) OVER (ORDER BY category RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
//	FROM t ORDER BY category, val
//
// Expected (RANGE mode — peer-group semantics):
//
//	Both rows with category='A' should have the same running SUM that
//	includes ALL rows with category <= 'A' (i.e., both A-rows).
//	  row 1 (category='A', val=1): SUM should be 3  (1+2, all A peers)
//	  row 2 (category='A', val=2): SUM should be 3  (1+2, all A peers)
//	  row 3 (category='B', val=4): SUM should be 7  (1+2+4)
//
// Actual (ROWS-like behaviour due to bug):
//
//	Each row's SUM stops at its own physical position, so the two A-rows
//	get different sums (1 and 3 respectively) rather than both getting 3.
func TestProbe_BUG004_RangeModeIgnored(t *testing.T) {
	// Build a minimal in-memory DB with a single small table.
	cat := catalog.New()
	store := storage.New()

	require.NoError(t, store.CreateTable("t"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "t",
		Columns: []catalog.Column{
			{Name: "category", Type: catalog.TypeText, Index: 0},
			{Name: "val", Type: catalog.TypeInt, Index: 1},
		},
	}))

	db := &testDB{cat: cat, store: store}

	// Insert three rows: two with category='A', one with category='B'.
	db.run(t, `INSERT INTO t (category, val) VALUES ('A', 1)`)
	db.run(t, `INSERT INTO t (category, val) VALUES ('A', 2)`)
	db.run(t, `INSERT INTO t (category, val) VALUES ('B', 4)`)

	// Run the window query using RANGE mode.
	result := db.run(t,
		`SELECT category, val, SUM(val) OVER (ORDER BY category RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS running_sum
		 FROM t
		 ORDER BY category, val`)

	require.Len(t, result.Rows, 3, "expected exactly 3 rows")

	// Collect actual running sums.
	actualSums := make([]float64, 3)
	for i, row := range result.Rows {
		actualSums[i] = row[2].FloatVal
	}

	t.Logf("Row 0 (category=%s, val=%v): running_sum=%v",
		result.Rows[0][0].StrVal, result.Rows[0][1].IntVal, result.Rows[0][2].FloatVal)
	t.Logf("Row 1 (category=%s, val=%v): running_sum=%v",
		result.Rows[1][0].StrVal, result.Rows[1][1].IntVal, result.Rows[1][2].FloatVal)
	t.Logf("Row 2 (category=%s, val=%v): running_sum=%v",
		result.Rows[2][0].StrVal, result.Rows[2][1].IntVal, result.Rows[2][2].FloatVal)

	// In correct RANGE mode, both A-rows must produce running_sum=3 (peer group).
	// In buggy ROWS-like mode, the first A-row produces 1 and the second produces 3.
	assert.Equal(t, float64(3), actualSums[0],
		"BUG-004: RANGE mode should include all peers; row 0 (category=A) expected running_sum=3, got %v", actualSums[0])
	assert.Equal(t, float64(3), actualSums[1],
		"BUG-004: RANGE mode should include all peers; row 1 (category=A) expected running_sum=3, got %v", actualSums[1])
	assert.Equal(t, float64(7), actualSums[2],
		"BUG-004: row 2 (category=B) expected running_sum=7, got %v", actualSums[2])
}
