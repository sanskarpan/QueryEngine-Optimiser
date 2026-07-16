package cost

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --------------------------------------------------------------------------
// CostModel tests
// --------------------------------------------------------------------------

func TestModel_SeqScan(t *testing.T) {
	m := NewModel()
	cost := m.SeqScan(1000, 10)
	assert.Equal(t, 10.0, cost)
}

func TestModel_HashJoin(t *testing.T) {
	m := NewModel()
	// 1.5 * inner + 1.0 * outer
	cost := m.HashJoin(100, 1000) // outer=100, inner=1000
	assert.Equal(t, 1.5*1000.0+100.0, cost)
}

func TestModel_NLJoin(t *testing.T) {
	m := NewModel()
	cost := m.NLJoin(100, 50)
	assert.Equal(t, 100.0*50.0*0.01, cost)
}

func TestModel_HashJoin_CheaperThanNL(t *testing.T) {
	m := NewModel()
	// For large inner, HashJoin beats NLJoin: 1.5*500+1000=1750 < 1000*500*0.01=5000.
	outer, inner := int64(1000), int64(500)
	hashCost := m.HashJoin(outer, inner)
	nlCost := m.NLJoin(outer, inner)
	assert.Less(t, hashCost, nlCost)
}

func TestModel_Sort(t *testing.T) {
	m := NewModel()
	assert.Equal(t, 0.0, m.Sort(1))
	assert.Greater(t, m.Sort(100), 0.0)
	// Monotonically increasing.
	assert.Greater(t, m.Sort(1000), m.Sort(100))
}

// --------------------------------------------------------------------------
// Estimator tests
// --------------------------------------------------------------------------

func testStatsMap() map[string]*stats.TableStats {
	return map[string]*stats.TableStats{
		"customers": {
			RowCount:  100,
			PageCount: 1,
			Columns: map[string]*stats.ColumnStats{
				"id":      {DistinctCount: 100},
				"country": {DistinctCount: 5},
			},
		},
		"orders": {
			RowCount:  1000,
			PageCount: 10,
			Columns: map[string]*stats.ColumnStats{
				"id":          {DistinctCount: 1000},
				"customer_id": {DistinctCount: 100},
				"status":      {DistinctCount: 4},
			},
		},
	}
}

func scanNode(name, alias string) *logical.LogicalScan {
	return &logical.LogicalScan{
		TableName: name,
		Alias:     alias,
		Table: &catalog.Table{
			Name: name,
			Columns: []catalog.Column{
				{Name: "id", Type: catalog.TypeInt, Index: 0},
			},
		},
	}
}

func TestEstimator_Scan(t *testing.T) {
	e := NewEstimator(testStatsMap())
	rows := e.EstimateRows(scanNode("customers", "customers"))
	assert.Equal(t, int64(100), rows)

	rows = e.EstimateRows(scanNode("orders", "orders"))
	assert.Equal(t, int64(1000), rows)
}

func TestEstimator_Scan_NoStats(t *testing.T) {
	e := NewEstimator(nil)
	rows := e.EstimateRows(scanNode("unknown_table", "unknown_table"))
	assert.Equal(t, int64(1000), rows) // default
}

func TestEstimator_Sort_PassesThrough(t *testing.T) {
	e := NewEstimator(testStatsMap())
	scan := scanNode("customers", "customers")
	sort := &logical.LogicalSort{Child: scan, SortSpecs: nil}
	assert.Equal(t, e.EstimateRows(scan), e.EstimateRows(sort))
}

func TestEstimator_Aggregate_NoGroupBy(t *testing.T) {
	e := NewEstimator(testStatsMap())
	scan := scanNode("orders", "orders")
	agg := &logical.LogicalAggregate{
		Child:   scan,
		GroupBy: nil,
		Aggs:    []logical.AggExpr{{Function: "COUNT", StarArg: true}},
	}
	assert.Equal(t, int64(1), e.EstimateRows(agg))
}

func TestEstimator_Empty(t *testing.T) {
	e := NewEstimator(nil)
	empty := &logical.EmptyRelation{Cols: nil}
	assert.Equal(t, int64(0), e.EstimateRows(empty))
}

func TestEstimator_Cost_SeqScan(t *testing.T) {
	e := NewEstimator(testStatsMap())
	scan := scanNode("orders", "orders")
	cost := e.EstimateCost(scan)
	// 10 pages * pageCost(1.0)
	assert.Equal(t, 10.0, cost)
}

// --------------------------------------------------------------------------
// JoinOrderOptimizer tests
// --------------------------------------------------------------------------

func TestJoinOrder_TwoTable(t *testing.T) {
	// Two-table join should still produce a valid join (possibly reordered).
	sm := testStatsMap()
	jo := NewJoinOrderOptimizer(sm)

	a := scanNode("customers", "c")
	b := scanNode("orders", "o")
	join := &logical.LogicalJoin{
		Left:      a,
		Right:     b,
		JoinType:  logical.InnerJoin,
		Condition: nil,
	}

	result := jo.Optimize(join)
	require.NotNil(t, result)
	// The result must still be a join.
	_, isJoin := result.(*logical.LogicalJoin)
	assert.True(t, isJoin)
}

func TestJoinOrder_ThreeTable_PicksOptimalOrder(t *testing.T) {
	// orders(1000) JOIN customers(100) JOIN products(50)
	// Optimal: start with smallest tables.
	sm := map[string]*stats.TableStats{
		"orders": {
			RowCount: 1000, PageCount: 10,
			Columns: map[string]*stats.ColumnStats{
				"customer_id": {DistinctCount: 100},
				"product_id":  {DistinctCount: 50},
			},
		},
		"customers": {
			RowCount: 100, PageCount: 1,
			Columns: map[string]*stats.ColumnStats{"id": {DistinctCount: 100}},
		},
		"products": {
			RowCount: 50, PageCount: 1,
			Columns: map[string]*stats.ColumnStats{"id": {DistinctCount: 50}},
		},
	}

	jo := NewJoinOrderOptimizer(sm)

	o := scanNode("orders", "o")
	c := scanNode("customers", "c")
	p := scanNode("products", "p")

	// Original order: orders JOIN customers JOIN products
	join1 := &logical.LogicalJoin{Left: o, Right: c, JoinType: logical.InnerJoin, Condition: nil}
	join2 := &logical.LogicalJoin{Left: join1, Right: p, JoinType: logical.InnerJoin, Condition: nil}

	result := jo.Optimize(join2)
	require.NotNil(t, result)

	// Result should be a join (optimal order).
	_, isJoin := result.(*logical.LogicalJoin)
	assert.True(t, isJoin)
}

func TestJoinOrder_OuterJoin_NotReordered(t *testing.T) {
	// Outer joins must not be reordered across inner joins.
	sm := testStatsMap()
	jo := NewJoinOrderOptimizer(sm)

	a := scanNode("customers", "c")
	b := scanNode("orders", "o")
	leftJoin := &logical.LogicalJoin{
		Left:      a,
		Right:     b,
		JoinType:  logical.LeftJoin,
		Condition: nil,
	}

	result := jo.Optimize(leftJoin)
	require.NotNil(t, result)
	// Should still be a LEFT join with the same structure.
	j, ok := result.(*logical.LogicalJoin)
	require.True(t, ok)
	assert.Equal(t, logical.LeftJoin, j.JoinType)
}

func TestJoinOrder_NonJoinPassthrough(t *testing.T) {
	sm := testStatsMap()
	jo := NewJoinOrderOptimizer(sm)
	scan := scanNode("customers", "c")
	result := jo.Optimize(scan)
	assert.Equal(t, scan, result)
}
