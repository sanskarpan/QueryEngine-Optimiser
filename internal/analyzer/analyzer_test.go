package analyzer

import (
	"testing"

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

func analyzeSQL(t *testing.T, sql string) error {
	t.Helper()
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	return a.Analyze(stmt)
}

func TestAnalyzer_ValidSimpleSelect(t *testing.T) {
	err := analyzeSQL(t, "SELECT id, name FROM customers")
	assert.NoError(t, err)
}

func TestAnalyzer_ValidSelectStar(t *testing.T) {
	err := analyzeSQL(t, "SELECT * FROM customers")
	assert.NoError(t, err)
}

func TestAnalyzer_UnknownTable(t *testing.T) {
	err := analyzeSQL(t, "SELECT id FROM nonexistent")
	assert.Error(t, err)
}

func TestAnalyzer_UnknownColumn(t *testing.T) {
	err := analyzeSQL(t, "SELECT missing_col FROM customers")
	assert.Error(t, err)
}

func TestAnalyzer_QualifiedColumn_Valid(t *testing.T) {
	err := analyzeSQL(t, "SELECT c.id, c.name FROM customers c")
	assert.NoError(t, err)
}

func TestAnalyzer_QualifiedColumn_WrongAlias(t *testing.T) {
	err := analyzeSQL(t, "SELECT x.id FROM customers c")
	assert.Error(t, err)
}

func TestAnalyzer_JoinValid(t *testing.T) {
	err := analyzeSQL(t, "SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id")
	assert.NoError(t, err)
}

func TestAnalyzer_AggregateInWhere_Error(t *testing.T) {
	err := analyzeSQL(t, "SELECT id FROM orders WHERE COUNT(*) > 5")
	assert.Error(t, err)
}

func TestAnalyzer_MixedAggNonAgg_Error(t *testing.T) {
	err := analyzeSQL(t, "SELECT id, COUNT(*) FROM orders")
	assert.Error(t, err)
}

func TestAnalyzer_GroupByWithAgg_Valid(t *testing.T) {
	err := analyzeSQL(t, "SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id")
	assert.NoError(t, err)
}

func TestAnalyzer_Having_Valid(t *testing.T) {
	err := analyzeSQL(t, "SELECT customer_id, SUM(amount) FROM orders GROUP BY customer_id HAVING SUM(amount) > 100")
	assert.NoError(t, err)
}

func TestAnalyzer_WhereColumnResolved(t *testing.T) {
	p := parser.New("SELECT id FROM customers WHERE country = 'US'")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)

	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	require.NoError(t, err)
}

func TestAnalyzer_ColumnAnnotation(t *testing.T) {
	p := parser.New("SELECT c.id FROM customers c WHERE c.id > 5")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)

	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	require.NoError(t, err)
}

func TestAnalyzer_AmbiguousColumn(t *testing.T) {
	// Both customers and orders have an 'id' column
	err := analyzeSQL(t, "SELECT id FROM customers c JOIN orders o ON c.id = o.customer_id")
	assert.Error(t, err)
}

func TestAnalyzer_Subquery_IN(t *testing.T) {
	err := analyzeSQL(t, "SELECT * FROM customers WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')")
	assert.NoError(t, err)
}

func TestAnalyzer_CreateTable_Valid(t *testing.T) {
	p := parser.New("CREATE TABLE tmp (id INT PRIMARY KEY, name TEXT NOT NULL)")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	assert.NoError(t, err)
}

func TestAnalyzer_CreateTable_DuplicateColumn(t *testing.T) {
	p := parser.New("CREATE TABLE tmp (id INT, id TEXT)")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	assert.Error(t, err)
}

func TestAnalyzer_Insert_Valid(t *testing.T) {
	p := parser.New("INSERT INTO customers (id, name, country) VALUES (1, 'Alice', 'US')")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	assert.NoError(t, err)
}

func TestAnalyzer_Insert_ColumnCountMismatch(t *testing.T) {
	p := parser.New("INSERT INTO customers (id, name) VALUES (1)")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	assert.Error(t, err)
}

func TestAnalyzer_Insert_UnknownTable(t *testing.T) {
	p := parser.New("INSERT INTO missing (id) VALUES (1)")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := New(makeTestCatalog())
	err = a.Analyze(stmt)
	assert.Error(t, err)
}
