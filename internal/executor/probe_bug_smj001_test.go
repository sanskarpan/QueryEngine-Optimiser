package executor

// TestProbe_BUG001_SMJNoSortNodes probes BUG-001: physical/builder.go lines 67-68
// select SortMergeJoin based on cost but never insert Sort nodes to pre-sort either
// input. The SortMergeJoin operator receives arbitrarily ordered input from its children.
//
// The physical plan emitted is:
//
//	SortMergeJoin
//	  SeqScan(a)
//	  SeqScan(b)
//
// rather than the semantically-correct:
//
//	SortMergeJoin
//	  Sort(a ON a.id)
//	  Sort(b ON b.id)
//
// The operator claims to sort internally (sort_merge_join.go Open() lines 107-114),
// but the internal sort uses pre-computed key indices with sort.SliceStable. Because
// sort.SliceStable passes CURRENT (post-swap) indices to the comparator while the key
// array still holds keys indexed by ORIGINAL positions, the sort compares wrong keys
// as elements move — producing incorrect row order. The subsequent merge then misses
// matching pairs.
//
// Repro: both tables have ids {1,2,3,4,5} inserted in reverse/shuffled order.
// All 5 ids exist in both tables, so the join should produce 5 rows.
// With the bug the operator returns only 1 row (id=5).

import (
	"testing"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/executor/operators"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG001_SMJNoSortNodes(t *testing.T) {
	cat := catalog.New()
	store := storage.New()

	// Table smj_a(id INT) — inserted in descending order: 5,3,1,4,2 (NOT sorted).
	tblA := &catalog.Table{
		Name: "smj_a",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
		},
	}
	require.NoError(t, cat.Register(tblA))
	require.NoError(t, store.CreateTable("smj_a"))
	heapA := store.MustGetTable("smj_a")
	for _, id := range []int64{5, 3, 1, 4, 2} {
		heapA.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(id)}})
	}

	// Table smj_b(id INT) — inserted in shuffled order: 4,2,5,1,3 (NOT sorted).
	tblB := &catalog.Table{
		Name: "smj_b",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
		},
	}
	require.NoError(t, cat.Register(tblB))
	require.NoError(t, store.CreateTable("smj_b"))
	heapB := store.MustGetTable("smj_b")
	for _, id := range []int64{4, 2, 5, 1, 3} {
		heapB.Insert(storage.Tuple{Values: []catalog.Value{catalog.IntValue(id)}})
	}

	// Build join condition: smj_a.id = smj_b.id
	condition := &ast.BinaryExpr{
		Op: lexer.Token{Type: lexer.EQ},
		Left: &ast.ColumnRef{
			Table:         "smj_a",
			ResolvedTable: "smj_a",
			Column:        "id",
		},
		Right: &ast.ColumnRef{
			Table:         "smj_b",
			ResolvedTable: "smj_b",
			Column:        "id",
		},
	}

	// Directly construct the SortMergeJoin operator — no Sort nodes are wrapped
	// around either child (exactly as physical/builder.go:67-68 produces).
	leftScan := &operators.SeqScan{TableName: "smj_a", Alias: "smj_a", Table: tblA}
	rightScan := &operators.SeqScan{TableName: "smj_b", Alias: "smj_b", Table: tblB}

	smjOp := &operators.SortMergeJoin{
		Left:      leftScan,
		Right:     rightScan,
		JoinType:  physical.InnerJoin,
		Condition: condition,
	}

	ctx := exectypes.NewExecContext(cat, store)
	require.NoError(t, smjOp.Open(ctx), "SortMergeJoin.Open should succeed")
	defer smjOp.Close()

	var rows [][]catalog.Value
	for {
		tuple, err := smjOp.Next()
		require.NoError(t, err, "SortMergeJoin.Next should not return an error")
		if tuple == nil {
			break
		}
		rows = append(rows, tuple.Values)
		t.Logf("SMJ returned row: a.id=%v  b.id=%v", tuple.Values[0], tuple.Values[1])
	}

	t.Logf("SortMergeJoin returned %d rows (expected 5)", len(rows))

	// Both tables have ids 1-5. All 5 pairs must be returned.
	// With BUG-001, the internal sort in Open() is broken because sort.SliceStable
	// passes current (post-swap) indices to the comparator while op.leftKeys still
	// holds pre-sorted-position values, causing wrong key comparisons mid-sort.
	// The broken sort produces incorrect ordering, and the merge misses 4 of 5 pairs,
	// returning only [[5, 5]].
	assert.Equal(t, 5, len(rows),
		"BUG-001: SortMergeJoin with unsorted input returned %d rows instead of 5. "+
			"The operator sorts internally but the sort uses pre-computed key indices "+
			"with sort.SliceStable, which compares wrong keys as elements move mid-sort. "+
			"Fix: sort an index array, or evaluate keys inline in the comparator, "+
			"or inject Sort nodes in physical/builder.go:67-68.",
		len(rows))

	// Each returned row must have equal join keys.
	seenIDs := make(map[int64]bool)
	for _, row := range rows {
		require.Len(t, row, 2, "each row should have 2 columns (smj_a.id, smj_b.id)")
		leftID := row[0].IntVal
		rightID := row[1].IntVal
		assert.Equal(t, leftID, rightID,
			"join key mismatch: smj_a.id=%d, smj_b.id=%d", leftID, rightID)
		seenIDs[leftID] = true
	}
	for id := int64(1); id <= 5; id++ {
		assert.True(t, seenIDs[id], "expected id=%d in join result", id)
	}
}
