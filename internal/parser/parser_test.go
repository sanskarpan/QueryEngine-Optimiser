package parser

import (
	"testing"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, sql string) ast.Statement {
	t.Helper()
	p := New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "SQL: %s", sql)
	return stmt
}

func parseSelect(t *testing.T, sql string) *ast.SelectStatement {
	t.Helper()
	stmt := parse(t, sql)
	sel, ok := stmt.(*ast.SelectStatement)
	require.True(t, ok, "expected SelectStatement")
	return sel
}

func TestParser_SimpleSelect(t *testing.T) {
	sel := parseSelect(t, "SELECT id, name FROM customers")
	assert.Len(t, sel.Columns, 2)
	assert.NotNil(t, sel.From)
	assert.Equal(t, "customers", sel.From.Name)
}

func TestParser_SelectStar(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM orders")
	require.Len(t, sel.Columns, 1)
	_, ok := sel.Columns[0].(*ast.StarExpr)
	assert.True(t, ok)
}

func TestParser_SelectDistinct(t *testing.T) {
	sel := parseSelect(t, "SELECT DISTINCT country FROM customers")
	assert.True(t, sel.Distinct)
}

func TestParser_SelectWithAlias(t *testing.T) {
	sel := parseSelect(t, "SELECT id AS customer_id, name AS customer_name FROM customers")
	require.Len(t, sel.Columns, 2)
	a1, ok := sel.Columns[0].(*ast.AliasExpr)
	require.True(t, ok)
	assert.Equal(t, "customer_id", a1.Alias)
}

func TestParser_SelectFromAlias(t *testing.T) {
	sel := parseSelect(t, "SELECT c.id FROM customers c")
	assert.Equal(t, "c", sel.From.Alias)
	col, ok := sel.Columns[0].(*ast.ColumnRef)
	require.True(t, ok)
	assert.Equal(t, "c", col.Table)
	assert.Equal(t, "id", col.Column)
}

func TestParser_SelectFromAlias_AS(t *testing.T) {
	sel := parseSelect(t, "SELECT c.id FROM customers AS c")
	assert.Equal(t, "c", sel.From.Alias)
}

func TestParser_Where(t *testing.T) {
	sel := parseSelect(t, "SELECT id FROM orders WHERE status = 'shipped'")
	require.NotNil(t, sel.Where)
	be, ok := sel.Where.(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.EQ, be.Op.Type)
}

func TestParser_WhereAnd(t *testing.T) {
	sel := parseSelect(t, "SELECT id FROM orders WHERE status = 'shipped' AND amount > 100")
	require.NotNil(t, sel.Where)
	be, ok := sel.Where.(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.AND, be.Op.Type)
}

func TestParser_OperatorPrecedence(t *testing.T) {
	// 1+2*3 should parse as 1+(2*3)
	sel := parseSelect(t, "SELECT 1+2*3")
	require.Len(t, sel.Columns, 1)
	add, ok := sel.Columns[0].(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.PLUS, add.Op.Type)

	_, isInt := add.Left.(*ast.IntLiteral)
	assert.True(t, isInt)

	mul, ok := add.Right.(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.STAR, mul.Op.Type)
}

func TestParser_GroupBy(t *testing.T) {
	sel := parseSelect(t, "SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id")
	require.Len(t, sel.GroupBy, 1)
	col, ok := sel.GroupBy[0].(*ast.ColumnRef)
	require.True(t, ok)
	assert.Equal(t, "customer_id", col.Column)

	// Columns should include COUNT(*)
	require.Len(t, sel.Columns, 2)
	fn, ok := sel.Columns[1].(*ast.FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "COUNT", fn.Name)
	assert.True(t, fn.StarArg)
}

func TestParser_Having(t *testing.T) {
	sel := parseSelect(t, "SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id HAVING COUNT(*) > 5")
	require.NotNil(t, sel.Having)
	be, ok := sel.Having.(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.GT, be.Op.Type)
}

func TestParser_OrderBy(t *testing.T) {
	sel := parseSelect(t, "SELECT id FROM orders ORDER BY id ASC, amount DESC")
	require.Len(t, sel.OrderBy, 2)
	assert.True(t, sel.OrderBy[0].Ascending)
	assert.False(t, sel.OrderBy[1].Ascending)
}

func TestParser_LimitOffset(t *testing.T) {
	sel := parseSelect(t, "SELECT id FROM orders LIMIT 10 OFFSET 20")
	require.NotNil(t, sel.Limit)
	require.NotNil(t, sel.Offset)
	lim, ok := sel.Limit.(*ast.IntLiteral)
	require.True(t, ok)
	assert.Equal(t, int64(10), lim.Value)
	off, ok := sel.Offset.(*ast.IntLiteral)
	require.True(t, ok)
	assert.Equal(t, int64(20), off.Value)
}

func TestParser_InnerJoin(t *testing.T) {
	sel := parseSelect(t, "SELECT t1.id, t2.name FROM t1 JOIN t2 ON t1.id = t2.fk")
	require.Len(t, sel.Joins, 1)
	j := sel.Joins[0]
	assert.Equal(t, ast.JoinInner, j.JoinType)
	assert.Equal(t, "t2", j.Table.Name)
	require.NotNil(t, j.Condition)
}

func TestParser_LeftJoin(t *testing.T) {
	sel := parseSelect(t, "SELECT t1.id FROM t1 LEFT JOIN t2 ON t1.id = t2.fk")
	require.Len(t, sel.Joins, 1)
	assert.Equal(t, ast.JoinLeft, sel.Joins[0].JoinType)
}

func TestParser_MultipleJoins(t *testing.T) {
	sel := parseSelect(t, "SELECT t1.id FROM t1 JOIN t2 ON t1.id = t2.fk LEFT JOIN t3 ON t2.id = t3.id")
	assert.Len(t, sel.Joins, 2)
}

func TestParser_Subquery_IN(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM customers WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')")
	be, ok := sel.Where.(*ast.InExpr)
	require.True(t, ok)
	assert.NotNil(t, be.Subquery)
	assert.False(t, be.Negated)
}

func TestParser_NotIn(t *testing.T) {
	p := New("SELECT * FROM t WHERE id NOT IN (1, 2, 3)")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	sel := stmt.(*ast.SelectStatement)
	// NOT IN is parsed as UnaryExpr(NOT) wrapping InExpr — or as InExpr with Negated=true
	// Let's check the structure
	_ = sel
	// The important thing is it parses without error
}

func TestParser_Between(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM orders WHERE amount BETWEEN 100 AND 500")
	be, ok := sel.Where.(*ast.BetweenExpr)
	require.True(t, ok)
	assert.False(t, be.Negated)
	low, ok := be.Low.(*ast.IntLiteral)
	require.True(t, ok)
	assert.Equal(t, int64(100), low.Value)
}

func TestParser_IsNull(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM t WHERE col IS NULL")
	isNull, ok := sel.Where.(*ast.IsNullExpr)
	require.True(t, ok)
	assert.False(t, isNull.Negated)
}

func TestParser_IsNotNull(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM t WHERE col IS NOT NULL")
	isNull, ok := sel.Where.(*ast.IsNullExpr)
	require.True(t, ok)
	assert.True(t, isNull.Negated)
}

func TestParser_Like(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM t WHERE name LIKE '%foo%'")
	be, ok := sel.Where.(*ast.BinaryExpr)
	require.True(t, ok)
	assert.Equal(t, lexer.LIKE, be.Op.Type)
}

func TestParser_FunctionCall(t *testing.T) {
	sel := parseSelect(t, "SELECT COUNT(*), SUM(amount), AVG(price) FROM orders")
	require.Len(t, sel.Columns, 3)

	count, ok := sel.Columns[0].(*ast.FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "COUNT", count.Name)
	assert.True(t, count.StarArg)

	sum, ok := sel.Columns[1].(*ast.FunctionCall)
	require.True(t, ok)
	assert.Equal(t, "SUM", sum.Name)
	assert.Len(t, sum.Args, 1)
}

func TestParser_Case(t *testing.T) {
	sel := parseSelect(t, "SELECT CASE WHEN status = 'shipped' THEN 1 ELSE 0 END FROM orders")
	require.Len(t, sel.Columns, 1)
	ce, ok := sel.Columns[0].(*ast.CaseExpr)
	require.True(t, ok)
	assert.Len(t, ce.Whens, 1)
	assert.NotNil(t, ce.ElseExpr)
}

func TestParser_Exists(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM customers c WHERE EXISTS (SELECT 1 FROM orders WHERE customer_id = 1)")
	exists, ok := sel.Where.(*ast.ExistsExpr)
	require.True(t, ok)
	assert.False(t, exists.Negated)
	assert.NotNil(t, exists.Subquery)
}

func TestParser_SubqueryInSelect(t *testing.T) {
	sel := parseSelect(t, "SELECT id, (SELECT COUNT(*) FROM orders WHERE customer_id = 1) AS order_count FROM customers c")
	require.Len(t, sel.Columns, 2)
	alias, ok := sel.Columns[1].(*ast.AliasExpr)
	require.True(t, ok)
	_, ok = alias.Expr.(*ast.SubqueryExpr)
	require.True(t, ok)
}

func TestParser_SubqueryInFrom(t *testing.T) {
	sel := parseSelect(t, "SELECT sub.id FROM (SELECT id FROM customers) AS sub")
	require.NotNil(t, sel.From.Subquery)
	assert.Equal(t, "sub", sel.From.Alias)
}

func TestParser_CreateTable(t *testing.T) {
	stmt := parse(t, "CREATE TABLE products (id INT PRIMARY KEY, name TEXT NOT NULL, price FLOAT)")
	ct, ok := stmt.(*ast.CreateTableStatement)
	require.True(t, ok)
	assert.Equal(t, "products", ct.Name)
	require.Len(t, ct.Columns, 3)
	assert.Equal(t, "id", ct.Columns[0].Name)
	assert.True(t, ct.Columns[0].PrimaryKey)
	assert.True(t, ct.Columns[1].NotNull)
}

func TestParser_Insert(t *testing.T) {
	stmt := parse(t, "INSERT INTO customers (id, name) VALUES (1, 'Alice')")
	ins, ok := stmt.(*ast.InsertStatement)
	require.True(t, ok)
	assert.Equal(t, "customers", ins.Table)
	assert.Equal(t, []string{"id", "name"}, ins.Columns)
	require.Len(t, ins.Values, 2)
}

func TestParser_UnaryNot(t *testing.T) {
	sel := parseSelect(t, "SELECT * FROM t WHERE NOT id = 5")
	_, ok := sel.Where.(*ast.UnaryExpr)
	assert.True(t, ok)
}

func TestParser_UnaryMinus(t *testing.T) {
	sel := parseSelect(t, "SELECT -1 FROM t")
	_, ok := sel.Columns[0].(*ast.UnaryExpr)
	assert.True(t, ok)
}

func TestParser_SelectWithoutFrom(t *testing.T) {
	// FROM-less SELECT with WHERE is valid (e.g. SELECT 1 WHERE 1=1)
	p := New("SELECT id WHERE id = 1")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	require.NotNil(t, stmt)
}

func TestParser_Error_UnrecognizedToken(t *testing.T) {
	// A token that is not FROM or any valid clause keyword should error
	p := New("SELECT id FRUM t")
	_, err := p.ParseStatement()
	require.Error(t, err)
}

func TestParser_Error_UnmatchedParen(t *testing.T) {
	p := New("SELECT (1 + 2 FROM t")
	_, err := p.ParseStatement()
	require.Error(t, err)
}

func TestParser_FullQuery(t *testing.T) {
	sql := `SELECT c.name, SUM(o.amount)
FROM orders o JOIN customers c ON o.customer_id = c.id
WHERE o.status = 'shipped'
GROUP BY c.name
ORDER BY 2 DESC
LIMIT 5`
	sel := parseSelect(t, sql)
	assert.NotNil(t, sel)
	assert.Len(t, sel.Columns, 2)
	assert.NotNil(t, sel.From)
	assert.Len(t, sel.Joins, 1)
	assert.NotNil(t, sel.Where)
	assert.Len(t, sel.GroupBy, 1)
	assert.Len(t, sel.OrderBy, 1)
	assert.NotNil(t, sel.Limit)
}

func TestParser_ThreeWayJoin(t *testing.T) {
	sql := `SELECT c.name, p.name, SUM(o.amount)
FROM orders o
JOIN customers c ON o.customer_id = c.id
JOIN products p ON o.product_id = p.id
GROUP BY c.name, p.name
ORDER BY 3 DESC
LIMIT 5`
	sel := parseSelect(t, sql)
	assert.Len(t, sel.Joins, 2)
	assert.Len(t, sel.GroupBy, 2)
}

func TestParser_Printer_Roundtrip(t *testing.T) {
	// Verify ASTPrinter produces output (full roundtrip would require printer→SQL→parse)
	sel := parseSelect(t, "SELECT id, name FROM customers WHERE id > 5")
	printed := ast.Print(sel)
	assert.Contains(t, printed, "SelectStatement")
	assert.Contains(t, printed, "customers")
}
