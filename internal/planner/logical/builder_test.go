package logical

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestCatalog() *catalog.Catalog {
	cat := catalog.New()
	cat.MustRegister(&catalog.Table{
		Name: "customers",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "name", Type: catalog.TypeText, Index: 1},
			{Name: "country", Type: catalog.TypeText, Index: 2},
		},
	})
	cat.MustRegister(&catalog.Table{
		Name: "orders",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "customer_id", Type: catalog.TypeInt, Index: 1},
			{Name: "amount", Type: catalog.TypeFloat, Index: 2},
			{Name: "status", Type: catalog.TypeText, Index: 3},
		},
	})
	cat.MustRegister(&catalog.Table{
		Name: "products",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "name", Type: catalog.TypeText, Index: 1},
			{Name: "price", Type: catalog.TypeFloat, Index: 2},
		},
	})
	return cat
}

// buildFromSQL parses, analyzes, and builds a logical plan.
func buildFromSQL(t *testing.T, sql string, cat *catalog.Catalog) Plan {
	t.Helper()
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err)

	a := analyzer.New(cat)
	err = a.Analyze(stmt)
	require.NoError(t, err)

	b := NewBuilder(cat)
	sel := stmt.(*ast.SelectStatement)
	plan, err := b.Build(sel)
	require.NoError(t, err)
	return plan
}

func TestBuilder_SimpleScan(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t, "SELECT id FROM customers", cat)
	require.NotNil(t, plan)

	proj, ok := plan.(*LogicalProject)
	require.True(t, ok, "expected LogicalProject at root, got %T", plan)

	_, ok = proj.Child.(*LogicalScan)
	require.True(t, ok, "expected LogicalScan below project, got %T", proj.Child)
}

func TestBuilder_Filter(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t, "SELECT id FROM customers WHERE id > 5", cat)

	proj := plan.(*LogicalProject)
	filter, ok := proj.Child.(*LogicalFilter)
	require.True(t, ok, "expected LogicalFilter below project, got %T", proj.Child)
	assert.NotNil(t, filter.Predicate)
}

func TestBuilder_Join(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t,
		"SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id", cat)

	proj := plan.(*LogicalProject)
	join, ok := proj.Child.(*LogicalJoin)
	require.True(t, ok, "expected LogicalJoin below project, got %T", proj.Child)
	assert.Equal(t, InnerJoin, join.JoinType)
}

func TestBuilder_GroupBy(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t,
		"SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id", cat)

	proj := plan.(*LogicalProject)
	agg, ok := proj.Child.(*LogicalAggregate)
	require.True(t, ok, "expected LogicalAggregate below project, got %T", proj.Child)
	assert.Len(t, agg.GroupBy, 1)
	assert.Len(t, agg.Aggs, 1)
}

func TestBuilder_OrderByLimit(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t, "SELECT id FROM customers ORDER BY id DESC LIMIT 5", cat)

	limit, ok := plan.(*LogicalLimit)
	require.True(t, ok, "expected LogicalLimit at root, got %T", plan)

	sort, ok := limit.Child.(*LogicalSort)
	require.True(t, ok, "expected LogicalSort below limit, got %T", limit.Child)
	assert.Len(t, sort.SortSpecs, 1)
	assert.False(t, sort.SortSpecs[0].Ascending)
}

func TestBuilder_Schema_Propagation(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t, "SELECT id, name FROM customers", cat)
	schema := plan.Schema()
	require.Len(t, schema, 2)
}

func TestBuilder_PrintPlan(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t,
		"SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id WHERE o.amount > 100",
		cat)
	printed := PrintPlan(plan)
	assert.Contains(t, printed, "LogicalProject")
	assert.Contains(t, printed, "LogicalFilter")
	assert.Contains(t, printed, "LogicalJoin")
}

func TestBuilder_Having(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t,
		"SELECT customer_id, SUM(amount) FROM orders GROUP BY customer_id HAVING SUM(amount) > 100",
		cat)

	// Root should be Project
	proj, ok := plan.(*LogicalProject)
	require.True(t, ok)
	// Below project: sort or limit or having filter
	// Structure: Project → Filter(HAVING) → Aggregate → Scan
	filter, ok := proj.Child.(*LogicalFilter)
	require.True(t, ok, "expected HAVING filter below project, got %T", proj.Child)
	agg, ok := filter.Child.(*LogicalAggregate)
	require.True(t, ok, "expected aggregate below having filter, got %T", filter.Child)
	assert.Len(t, agg.GroupBy, 1)
}

func TestBuilder_ThreeWayJoin(t *testing.T) {
	cat := makeTestCatalog()
	plan := buildFromSQL(t,
		`SELECT c.name, p.name, SUM(o.amount)
		FROM orders o
		JOIN customers c ON o.customer_id = c.id
		JOIN products p ON o.id = p.id
		GROUP BY c.name, p.name
		ORDER BY 3 DESC
		LIMIT 5`,
		cat)

	limit, ok := plan.(*LogicalLimit)
	require.True(t, ok, "expected limit at root, got %T", plan)
	sort, ok := limit.Child.(*LogicalSort)
	require.True(t, ok)
	proj, ok := sort.Child.(*LogicalProject)
	require.True(t, ok)
	agg, ok := proj.Child.(*LogicalAggregate)
	require.True(t, ok)
	assert.Len(t, agg.GroupBy, 2)
	// Scan chain: join(join(orders, customers), products)
	_ = agg.Child
}
