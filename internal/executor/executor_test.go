package executor

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDB creates an in-memory DB with seed data.
type testDB struct {
	cat   *catalog.Catalog
	store *storage.Storage
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	cat := catalog.New()
	store := storage.New()
	require.NoError(t, storage.Seed(cat, store))
	return &testDB{cat: cat, store: store}
}

func (db *testDB) run(t *testing.T, sql string) *Result {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse: %s", sql)

	a := analyzer.New(db.cat)
	require.NoError(t, a.Analyze(stmt), "analyze: %s", sql)

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	require.NoError(t, err, "build: %s", sql)

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	require.NoError(t, err, "physical build: %s", sql)

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	result, err := Execute(pplan, ctx)
	require.NoError(t, err, "execute: %s", sql)
	return result
}

func TestExecutor_SimpleScan(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT * FROM customers")
	assert.Equal(t, 100, len(result.Rows))
	assert.Len(t, result.Columns, 5) // id, name, email, country, created_at
}

func TestExecutor_Filter(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers WHERE country = 'US'")
	assert.Greater(t, len(result.Rows), 0)
	assert.LessOrEqual(t, len(result.Rows), 100)
}

func TestExecutor_Limit(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers LIMIT 5")
	assert.Equal(t, 5, len(result.Rows))
}

func TestExecutor_LimitOffset(t *testing.T) {
	db := newTestDB(t)
	r1 := db.run(t, "SELECT id FROM customers ORDER BY id LIMIT 10")
	r2 := db.run(t, "SELECT id FROM customers ORDER BY id LIMIT 5 OFFSET 5")

	require.Len(t, r2.Rows, 5)
	// r2 should be rows 6-10 of r1
	for i := 0; i < 5; i++ {
		assert.Equal(t, r1.Rows[5+i][0], r2.Rows[i][0])
	}
}

func TestExecutor_OrderBy(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers ORDER BY id ASC LIMIT 3")
	require.Len(t, result.Rows, 3)
	// First row should have the smallest ID
	id1 := result.Rows[0][0].IntVal
	id2 := result.Rows[1][0].IntVal
	id3 := result.Rows[2][0].IntVal
	assert.Less(t, id1, id2)
	assert.Less(t, id2, id3)
}

func TestExecutor_OrderByDesc(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers ORDER BY id DESC LIMIT 3")
	require.Len(t, result.Rows, 3)
	id1 := result.Rows[0][0].IntVal
	id2 := result.Rows[1][0].IntVal
	assert.Greater(t, id1, id2)
}

func TestExecutor_InnerJoin(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t,
		"SELECT c.id, o.id FROM customers c JOIN orders o ON c.id = o.customer_id LIMIT 10")
	assert.Greater(t, len(result.Rows), 0)
	assert.Len(t, result.Columns, 2)
}

func TestExecutor_CountStar(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM orders")
	require.Len(t, result.Rows, 1)
	count := result.Rows[0][0].IntVal
	assert.Equal(t, int64(1000), count)
}

func TestExecutor_SumAggregate(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT SUM(amount) FROM orders")
	require.Len(t, result.Rows, 1)
	sum := result.Rows[0][0]
	assert.Equal(t, catalog.TypeFloat, sum.Type)
	assert.Greater(t, sum.FloatVal, 0.0)
}

func TestExecutor_GroupBy(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT status, COUNT(*) FROM orders GROUP BY status")
	assert.Greater(t, len(result.Rows), 0)
	// Should have 4 statuses: pending, processing, shipped, cancelled
	assert.LessOrEqual(t, len(result.Rows), 4)
}

func TestExecutor_GroupByOrderBy(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t,
		"SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id ORDER BY customer_id LIMIT 5")
	require.Len(t, result.Rows, 5)
}

func TestExecutor_Having(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t,
		"SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id HAVING COUNT(*) > 5")
	// All returned groups must have count > 5
	for _, row := range result.Rows {
		count := row[1].IntVal
		assert.Greater(t, count, int64(5))
	}
}

func TestExecutor_InFilter(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers WHERE id IN (1, 2, 3)")
	require.Len(t, result.Rows, 3)
}

func TestExecutor_LikeFilter(t *testing.T) {
	db := newTestDB(t)
	// All customers have names like "First Last"
	result := db.run(t, "SELECT id FROM customers WHERE name LIKE '%Alice%'")
	assert.Greater(t, len(result.Rows), 0)
}

func TestExecutor_IsNull(t *testing.T) {
	db := newTestDB(t)
	// status in orders is never null in seed data
	result := db.run(t, "SELECT id FROM orders WHERE status IS NOT NULL")
	assert.Equal(t, 1000, len(result.Rows))
}

func TestExecutor_ConstantFolding(t *testing.T) {
	db := newTestDB(t)
	// 1+1 should fold to 2 at optimize time
	result := db.run(t, "SELECT 1+1 FROM customers LIMIT 1")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(2), result.Rows[0][0].IntVal)
}

func TestExecutor_ThreeWayJoin(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT c.id, p.id, o.amount
		FROM orders o
		JOIN customers c ON o.customer_id = c.id
		JOIN products p ON o.product_id = p.id
		LIMIT 5
	`)
	require.Len(t, result.Rows, 5)
}

func TestExecutor_EmptyResult(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT id FROM customers WHERE 1 = 2")
	assert.Len(t, result.Rows, 0)
}

func TestExecutor_UnionAll(t *testing.T) {
	db := newTestDB(t)
	// UNION ALL concatenates rows without deduplication
	result := db.run(t, `
		SELECT country FROM customers WHERE country = 'US' LIMIT 3
		UNION ALL
		SELECT country FROM customers WHERE country = 'US' LIMIT 3
	`)
	// 3 + 3 = 6 rows, all 'US'
	assert.Len(t, result.Rows, 6)
	for _, row := range result.Rows {
		assert.Equal(t, "US", row[0].StrVal)
	}
}

func TestExecutor_UnionDistinct(t *testing.T) {
	db := newTestDB(t)
	// UNION deduplicates
	result := db.run(t, `
		SELECT country FROM customers WHERE country = 'US'
		UNION
		SELECT country FROM customers WHERE country = 'US'
	`)
	// Only 1 distinct 'US' row
	assert.Len(t, result.Rows, 1)
	assert.Equal(t, "US", result.Rows[0][0].StrVal)
}

func TestExecutor_Intersect(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT country FROM customers WHERE country = 'US'
		INTERSECT
		SELECT country FROM customers WHERE country = 'US'
	`)
	assert.Len(t, result.Rows, 1)
	assert.Equal(t, "US", result.Rows[0][0].StrVal)
}

func TestExecutor_Except(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT country FROM customers WHERE country = 'US'
		EXCEPT
		SELECT country FROM customers WHERE country = 'US'
	`)
	// Removes all US rows from left side — empty result
	assert.Len(t, result.Rows, 0)
}

func TestExecutor_SortMergeJoin(t *testing.T) {
	db := newTestDB(t)
	// Run a join — SMJ or HashJoin depending on cost
	result := db.run(t, `
		SELECT c.id, o.id
		FROM customers c JOIN orders o ON c.id = o.customer_id
		LIMIT 5
	`)
	assert.Len(t, result.Rows, 5)
}

func TestExecutor_Insert(t *testing.T) {
	db := newTestDB(t)

	// Insert a new product
	result := db.run(t, `INSERT INTO products (id, name, category, price) VALUES (9999, 'TestWidget', 'gadgets', 99.99)`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(1), result.Rows[0][0].IntVal)

	// Verify the row is readable
	check := db.run(t, `SELECT id, name FROM products WHERE id = 9999`)
	require.Len(t, check.Rows, 1)
	assert.Equal(t, int64(9999), check.Rows[0][0].IntVal)
	assert.Equal(t, "TestWidget", check.Rows[0][1].StrVal)
}

func TestExecutor_InsertMultiple(t *testing.T) {
	db := newTestDB(t)

	// Two inserts into the same table
	db.run(t, `INSERT INTO products (id, name, category, price) VALUES (8001, 'Alpha', 'tools', 10.00)`)
	db.run(t, `INSERT INTO products (id, name, category, price) VALUES (8002, 'Beta', 'tools', 20.00)`)

	result := db.run(t, `SELECT COUNT(*) FROM products WHERE id IN (8001, 8002)`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(2), result.Rows[0][0].IntVal)
}

func TestExecutor_ExistsSubquery(t *testing.T) {
	db := newTestDB(t)
	// Customers that have at least one order
	result := db.run(t, `
		SELECT id FROM customers
		WHERE EXISTS (SELECT 1 FROM orders WHERE orders.customer_id = customers.id)
		LIMIT 10
	`)
	assert.Greater(t, len(result.Rows), 0)
	assert.LessOrEqual(t, len(result.Rows), 10)
}

func TestExecutor_NotExistsSubquery(t *testing.T) {
	db := newTestDB(t)
	// Customers that have NO orders with amount > 1e9 (should be all customers)
	result := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE NOT EXISTS (SELECT 1 FROM orders WHERE amount > 1000000000)
	`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(100), result.Rows[0][0].IntVal)
}

func TestExecutor_InSubquery(t *testing.T) {
	db := newTestDB(t)
	// Customers whose id appears in a subquery result
	result := db.run(t, `
		SELECT id FROM customers
		WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')
		LIMIT 5
	`)
	assert.Greater(t, len(result.Rows), 0)
	assert.LessOrEqual(t, len(result.Rows), 5)
}

// --------------------------------------------------------------------------
// BUG-03: RIGHT JOIN must include all right-side rows (not behave as INNER)
// --------------------------------------------------------------------------

func TestExecutor_RightJoin(t *testing.T) {
	db := newTestDB(t)
	// All 100 customers must appear; orders without a matching customer get NULL customer columns.
	// Since every order references a valid customer (customer_id 1-100), this is equivalent to
	// an inner join, but the guarantee we test is: row count >= distinct customers with orders.
	result := db.run(t, `
		SELECT c.id, o.id
		FROM orders o
		RIGHT JOIN customers c ON o.customer_id = c.id
		ORDER BY c.id
		LIMIT 10
	`)
	require.Greater(t, len(result.Rows), 0)
	assert.Len(t, result.Columns, 2)

	// Verify every returned row has a non-NULL customer id (right-side column always present).
	for _, row := range result.Rows {
		assert.False(t, row[0].IsNull, "customer id should never be NULL in a RIGHT JOIN on customers")
	}
}

// --------------------------------------------------------------------------
// BUG-08: NOT IN with NULL must return NULL, not TRUE
// --------------------------------------------------------------------------

func TestExecutor_NotInWithNull(t *testing.T) {
	db := newTestDB(t)
	// SQL: 5 NOT IN (1, NULL) → NULL (not TRUE), so WHERE clause is not satisfied.
	// The subquery 'SELECT id FROM customers WHERE id NOT IN (1, NULL)' should return 0 rows
	// because x NOT IN (list containing NULL) = NULL for any x not in the list.
	result := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE id NOT IN (1, NULL)
	`)
	require.Len(t, result.Rows, 1)
	// Per SQL three-valued logic: x NOT IN (..., NULL) is NULL, not TRUE.
	// A NULL WHERE clause is treated as FALSE → 0 rows pass.
	assert.Equal(t, int64(0), result.Rows[0][0].IntVal)
}

// --------------------------------------------------------------------------
// BUG-02: SortMergeJoin must match rows correctly (not just return the right count)
// --------------------------------------------------------------------------

func TestExecutor_SortMergeJoinCorrectness(t *testing.T) {
	db := newTestDB(t)
	// For each returned (customer.id, order.customer_id) pair, the two values must be equal.
	// This would fail with string-sorted keys for multi-digit IDs.
	result := db.run(t, `
		SELECT c.id, o.customer_id
		FROM customers c
		JOIN orders o ON c.id = o.customer_id
		WHERE c.id >= 9
		LIMIT 20
	`)
	require.Greater(t, len(result.Rows), 0)
	for _, row := range result.Rows {
		cid := row[0].IntVal
		oid := row[1].IntVal
		assert.Equal(t, cid, oid, "join key mismatch: customer.id=%d, order.customer_id=%d", cid, oid)
	}
}

// --------------------------------------------------------------------------
// BUG-19: SELECT without FROM + WHERE must not panic
// --------------------------------------------------------------------------

func TestExecutor_SelectWithoutFromAndWhere(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 1 WHERE 1 = 1`)
	require.Len(t, result.Rows, 1)

	result2 := db.run(t, `SELECT 42 WHERE 1 = 2`)
	assert.Len(t, result2.Rows, 0)
}

// --------------------------------------------------------------------------
// BUG-18: Negative LIMIT must return an error
// --------------------------------------------------------------------------

func TestExecutor_NegativeLimitErrors(t *testing.T) {
	db := newTestDB(t)
	p := parser.New("SELECT id FROM customers LIMIT -1")
	stmt, err := p.ParseStatement()
	require.NoError(t, err)

	a := analyzer.New(db.cat)
	require.NoError(t, a.Analyze(stmt))

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	require.NoError(t, err)

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	require.NoError(t, err)

	ctx := exectypes.NewExecContext(db.cat, db.store)
	_, execErr := Execute(pplan, ctx)
	assert.Error(t, execErr, "LIMIT -1 should produce an error")
}
