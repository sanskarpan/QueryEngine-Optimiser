package executor

// TestProbe_BUG_FV001_FIRST_VALUE_IgnoresFrame probes the bug where FIRST_VALUE
// ignores the window frame specification entirely and always returns the value
// from the first row of the partition instead of the first row of the frame.
//
// Bug report: BUG-001 (FIRST_VALUE ignores window frame)
// Area: executor/operators/window.go — FIRST_VALUE case
//
// With frame ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING:
//   - For row at position i in the sorted partition, the frame starts at i.
//   - FIRST_VALUE should therefore return the value of the current row itself.
//   - Buggy behaviour: always returns the value from sortedIdxs[0] (partition row 0).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG_FV001_FIRST_VALUE_CurrentRowFrame verifies that
// FIRST_VALUE respects ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING.
// With that frame, FIRST_VALUE(val) for each row should equal val itself
// (because the frame's first row IS the current row).
func TestProbe_BUG_FV001_FIRST_VALUE_CurrentRowFrame(t *testing.T) {
	db := newTestDB(t)

	// Use a small, deterministic slice of customers ordered by id.
	// With ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING the frame
	// for each row starts at the current row, so FIRST_VALUE(id) == id.
	r := db.run(t, `
		SELECT id,
		       FIRST_VALUE(id) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS fv
		FROM customers
		ORDER BY id
		LIMIT 5
	`)

	require.Equal(t, 5, len(r.Rows), "expected 5 rows")

	for i, row := range r.Rows {
		rowID := row[0].IntVal
		fv := row[1].IntVal
		assert.Equal(t, rowID, fv,
			"row %d: FIRST_VALUE with CURRENT ROW frame should equal the row's own id (%d), got %d",
			i, rowID, fv)
	}
}

// TestProbe_BUG_FV001_FIRST_VALUE_NPrecedingFrame verifies FIRST_VALUE with
// ROWS BETWEEN 2 PRECEDING AND CURRENT ROW.
// For the 3rd row (i=2) onwards the frame starts 2 rows back; FIRST_VALUE should
// return the value two rows prior, NOT the absolute first row of the partition.
func TestProbe_BUG_FV001_FIRST_VALUE_NPrecedingFrame(t *testing.T) {
	db := newTestDB(t)

	// Fetch first 5 customer ids in order so we can compute expected values.
	ids := db.run(t, `SELECT id FROM customers ORDER BY id LIMIT 5`)
	require.Equal(t, 5, len(ids.Rows))

	r := db.run(t, `
		SELECT id,
		       FIRST_VALUE(id) OVER (ORDER BY id ROWS BETWEEN 2 PRECEDING AND CURRENT ROW) AS fv
		FROM customers
		ORDER BY id
		LIMIT 5
	`)
	require.Equal(t, 5, len(r.Rows), "expected 5 rows")

	// Build expected values manually:
	// position 0: frame [0,0] -> ids[0]
	// position 1: frame [0,1] -> ids[0]
	// position 2: frame [0,2] -> ids[0]
	// position 3: frame [1,3] -> ids[1]
	// position 4: frame [2,4] -> ids[2]
	expected := []int64{
		ids.Rows[0][0].IntVal, // frame start=max(0,0-2)=0 -> ids[0]
		ids.Rows[0][0].IntVal, // frame start=max(0,1-2)=0 -> ids[0]
		ids.Rows[0][0].IntVal, // frame start=max(0,2-2)=0 -> ids[0]
		ids.Rows[1][0].IntVal, // frame start=max(0,3-2)=1 -> ids[1]
		ids.Rows[2][0].IntVal, // frame start=max(0,4-2)=2 -> ids[2]
	}

	for i, row := range r.Rows {
		fv := row[1].IntVal
		assert.Equal(t, expected[i], fv,
			"row %d: FIRST_VALUE with 2 PRECEDING frame should be %d, got %d",
			i, expected[i], fv)
	}

	// The critical assertion: row 3 and row 4 MUST differ from the absolute
	// first row (ids[0]) if the frame is respected.
	// If the bug is present, all rows return ids[0].
	if len(r.Rows) >= 5 {
		absoluteFirst := ids.Rows[0][0].IntVal
		row3FV := r.Rows[3][1].IntVal
		row4FV := r.Rows[4][1].IntVal

		assert.NotEqual(t, absoluteFirst, row3FV,
			"BUG-001: row 3 FIRST_VALUE should NOT be the absolute partition-first row (%d); got %d",
			absoluteFirst, row3FV)
		assert.NotEqual(t, absoluteFirst, row4FV,
			"BUG-001: row 4 FIRST_VALUE should NOT be the absolute partition-first row (%d); got %d",
			absoluteFirst, row4FV)
	}
}
