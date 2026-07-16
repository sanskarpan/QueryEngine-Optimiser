package executor

// TestProbe_BUG002_LAST_VALUE_IgnoresFrame probes BUG-002:
// LAST_VALUE always evaluates against the absolute last row of the partition
// (sortedIdxs[len(sortedIdxs)-1]) and broadcasts that single value to every
// row in the partition, ignoring the window frame entirely.
//
// The SQL standard default frame is RANGE BETWEEN UNBOUNDED PRECEDING AND
// CURRENT ROW. With that frame, the frame for row at sorted position i is
// [0..i], so LAST_VALUE returns the current row's own value. The buggy
// implementation instead always returns the value at sortedIdxs[len-1].

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG002_LAST_VALUE_DefaultFrame verifies that LAST_VALUE with
// the default frame (RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
// returns each row's own value, not the absolute last partition value.
func TestProbe_BUG002_LAST_VALUE_DefaultFrame(t *testing.T) {
	// Use a fresh, isolated in-memory DB with data inserted via the storage API
	// directly (the logical planner does not support DDL statements).
	cat := catalog.New()
	store := storage.New()

	tbl := &catalog.Table{
		Name: "lv_t",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "val", Type: catalog.TypeInt, Index: 1},
		},
	}
	require.NoError(t, cat.Register(tbl))
	require.NoError(t, store.CreateTable("lv_t"))

	heap := store.MustGetTable("lv_t")
	heap.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(1), catalog.IntValue(10)}})
	heap.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(2), catalog.IntValue(20)}})
	heap.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(3), catalog.IntValue(30)}})

	db := &testDB{cat: cat, store: store}

	// With default frame RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW:
	//   sorted position 0 (id=1): frame=[0..0] → LAST_VALUE(val) = 10
	//   sorted position 1 (id=2): frame=[0..1] → LAST_VALUE(val) = 20
	//   sorted position 2 (id=3): frame=[0..2] → LAST_VALUE(val) = 30
	result := db.run(t, `SELECT id, val, LAST_VALUE(val) OVER (ORDER BY id) AS lv FROM lv_t ORDER BY id`)

	require.Equal(t, 3, len(result.Rows), "expected 3 rows")

	t.Logf("Result rows (id, val, LAST_VALUE):")
	for _, r := range result.Rows {
		t.Logf("  id=%d  val=%d  LAST_VALUE=%d", r[0].IntVal, r[1].IntVal, r[2].IntVal)
	}

	// Each row's LAST_VALUE should equal its own val (because the frame ends
	// at the current row). The buggy implementation returns 30 for all rows.
	for _, r := range result.Rows {
		id := r[0].IntVal
		val := r[1].IntVal
		lv := r[2].IntVal
		assert.Equal(t, val, lv,
			"id=%d: LAST_VALUE(val) with default frame should be %d (own val), got %d — frame is ignored",
			id, val, lv)
	}
}

// TestProbe_BUG002_LAST_VALUE_ExplicitFrame verifies that an explicitly
// specified frame is also honoured by LAST_VALUE.
//
// With ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING the frame for row i
// is [i..n-1], so LAST_VALUE always returns the absolute last partition value —
// but note that is the correct answer only for this explicit frame. Even here
// the bug masks itself accidentally. The more revealing explicit frame is
// ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING: LAST_VALUE should be the next
// row's val (or own val for the last row).
func TestProbe_BUG002_LAST_VALUE_ExplicitFollowingFrame(t *testing.T) {
	cat := catalog.New()
	store := storage.New()

	tbl2 := &catalog.Table{
		Name: "lv_t2",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "val", Type: catalog.TypeInt, Index: 1},
		},
	}
	require.NoError(t, cat.Register(tbl2))
	require.NoError(t, store.CreateTable("lv_t2"))

	heap2 := store.MustGetTable("lv_t2")
	heap2.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(1), catalog.IntValue(10)}})
	heap2.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(2), catalog.IntValue(20)}})
	heap2.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(3), catalog.IntValue(30)}})

	db := &testDB{cat: cat, store: store}

	// ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING:
	//   position 0 (id=1): frame=[0..1] → LAST_VALUE(val) = 20
	//   position 1 (id=2): frame=[1..2] → LAST_VALUE(val) = 30
	//   position 2 (id=3): frame=[2..2] → LAST_VALUE(val) = 30
	result := db.run(t, `
		SELECT id, val,
		       LAST_VALUE(val) OVER (ORDER BY id ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING) AS lv
		FROM lv_t2 ORDER BY id`)

	require.Equal(t, 3, len(result.Rows), "expected 3 rows")

	t.Logf("ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING result (id, val, LAST_VALUE):")
	for _, r := range result.Rows {
		t.Logf("  id=%d  val=%d  LAST_VALUE=%d", r[0].IntVal, r[1].IntVal, r[2].IntVal)
	}

	expected := []int64{20, 30, 30}
	for i, r := range result.Rows {
		lv := r[2].IntVal
		assert.Equal(t, expected[i], lv,
			"row %d (id=%d): LAST_VALUE with CURRENT ROW AND 1 FOLLOWING should be %d, got %d",
			i, r[0].IntVal, expected[i], lv)
	}
}
