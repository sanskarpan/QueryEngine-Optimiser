package executor

// TestProbe_Bug003_NTH_VALUE_IgnoresFrame verifies BUG-003:
// NTH_VALUE ignores the window frame. The implementation computes a fixed
// offset (nth) from the argument and applies it into the full sortedIdxs
// slice rather than recomputing the frame per row.
//
// Repro: NTH_VALUE(val, 1) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING)
// Expected: each row returns its own val (the 1st row within a frame that starts at CURRENT ROW)
// Actual (buggy): every row returns the val from sortedIdxs[0] — always the first row.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_Bug003_NTH_VALUE_IgnoresFrame(t *testing.T) {
	db := newTestDB(t)

	// Use a small slice of customers ordered by id.
	// NTH_VALUE(id, 1) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING)
	// should return the id of the CURRENT ROW for every row
	// (because the frame starts at current row, so the 1st row within the frame IS the current row).
	//
	// The buggy implementation uses a fixed nth=0 index into the full sortedIdxs slice,
	// so every row incorrectly returns the id of the first row in the partition.
	r := db.run(t, `
		SELECT id,
		       NTH_VALUE(id, 1) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS nth_val
		FROM customers
		ORDER BY id
		LIMIT 5
	`)

	require.Len(t, r.Rows, 5, "expected 5 rows")

	// Collect the actual ids (sorted ascending by id).
	// For each row, nth_val should equal that row's own id.
	firstID := r.Rows[0][0].IntVal // the smallest id

	for i, row := range r.Rows {
		rowID := row[0].IntVal
		nthVal := row[1].IntVal
		assert.Equal(t, rowID, nthVal,
			"row %d: NTH_VALUE(id,1) with CURRENT ROW frame start should return the current row's id (%d), got %d (bug: always returns first row's id %d)",
			i, rowID, nthVal, firstID)
	}
}
