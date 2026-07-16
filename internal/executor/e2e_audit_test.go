package executor

// Comprehensive E2E audit tests. Each test exercises a specific SQL feature or
// edge case, verifying both correct results and absence of panics/errors.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// DISTINCT
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Distinct_Countries(t *testing.T) {
	db := newTestDB(t)
	// 5 countries in seed data; DISTINCT must deduplicate
	result := db.run(t, "SELECT DISTINCT country FROM customers ORDER BY country")
	require.LessOrEqual(t, len(result.Rows), 5)
	require.Greater(t, len(result.Rows), 0)
	// Each row must be unique
	seen := map[string]bool{}
	for _, row := range result.Rows {
		v := row[0].StrVal
		assert.False(t, seen[v], "duplicate country %q in DISTINCT result", v)
		seen[v] = true
	}
}

func TestAudit_Distinct_Statuses(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT DISTINCT status FROM orders")
	// seed has: shipped, processing, pending, cancelled
	assert.LessOrEqual(t, len(result.Rows), 4)
	assert.Greater(t, len(result.Rows), 0)
}

func TestAudit_Distinct_vs_All(t *testing.T) {
	db := newTestDB(t)
	all := db.run(t, "SELECT country FROM customers")
	distinct := db.run(t, "SELECT DISTINCT country FROM customers")
	assert.Equal(t, 100, len(all.Rows))
	assert.Less(t, len(distinct.Rows), len(all.Rows), "DISTINCT must reduce row count")
}

// ─────────────────────────────────────────────────────────────────────────────
// LEFT JOIN – verify unmatched left rows emit NULLs, not get dropped
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_LeftJoin_UnmatchedRows(t *testing.T) {
	db := newTestDB(t)
	// Create a customer with id=999 that has no orders
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (999, 'NoOrder Customer', 'noorder@test.com', 'US', '2024-01-01')`)

	result := db.run(t, `
		SELECT c.id, o.id
		FROM customers c
		LEFT JOIN orders o ON c.id = o.customer_id
		WHERE c.id = 999
	`)
	// Must get exactly one row; the order id column must be NULL
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(999), result.Rows[0][0].IntVal)
	assert.True(t, result.Rows[0][1].IsNull, "order id must be NULL for unmatched customer")
}

func TestAudit_LeftJoin_MatchedRows(t *testing.T) {
	db := newTestDB(t)
	// Customer 1 has orders; LEFT JOIN must return their orders normally
	result := db.run(t, `
		SELECT c.id, o.id
		FROM customers c
		LEFT JOIN orders o ON c.id = o.customer_id
		WHERE c.id = 1
	`)
	require.Greater(t, len(result.Rows), 0)
	for _, row := range result.Rows {
		assert.False(t, row[1].IsNull, "order id must not be NULL for matched customer")
	}
}

func TestAudit_LeftJoin_AllCustomersPresent(t *testing.T) {
	db := newTestDB(t)
	// Add an unmatched customer and verify LEFT JOIN preserves them even without orders
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (998, 'Lonely', 'lonely@test.com', 'US', '2024-01-01')`)

	result := db.run(t, `
		SELECT c.id FROM customers c
		LEFT JOIN orders o ON c.id = o.customer_id
		WHERE c.id = 998
		LIMIT 1
	`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(998), result.Rows[0][0].IntVal, "unmatched customer must appear in LEFT JOIN")
}

// ─────────────────────────────────────────────────────────────────────────────
// BETWEEN
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Between_Integer(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id BETWEEN 10 AND 20")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(11), result.Rows[0][0].IntVal) // inclusive: 10,11,...,20
}

func TestAudit_Between_NotBetween(t *testing.T) {
	db := newTestDB(t)
	all := db.run(t, "SELECT COUNT(*) FROM customers").Rows[0][0].IntVal
	between := db.run(t, "SELECT COUNT(*) FROM customers WHERE id BETWEEN 10 AND 20").Rows[0][0].IntVal
	notBetween := db.run(t, "SELECT COUNT(*) FROM customers WHERE id NOT BETWEEN 10 AND 20").Rows[0][0].IntVal
	assert.Equal(t, all, between+notBetween, "BETWEEN and NOT BETWEEN must partition the rows")
}

func TestAudit_Between_Float(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM products WHERE price BETWEEN 10.0 AND 100.0")
	require.Len(t, result.Rows, 1)
	assert.GreaterOrEqual(t, result.Rows[0][0].IntVal, int64(0))
}

// ─────────────────────────────────────────────────────────────────────────────
// CASE WHEN
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_CaseWhen_Searched(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT CASE WHEN id < 50 THEN 'low' WHEN id < 80 THEN 'mid' ELSE 'high' END
		FROM customers ORDER BY id LIMIT 5
	`)
	require.Len(t, result.Rows, 5)
	for _, row := range result.Rows {
		v := row[0].StrVal
		assert.Contains(t, []string{"low", "mid", "high"}, v)
	}
}

func TestAudit_CaseWhen_Simple(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT CASE status WHEN 'shipped' THEN 1 WHEN 'pending' THEN 2 ELSE 0 END
		FROM orders LIMIT 5
	`)
	require.Len(t, result.Rows, 5)
	for _, row := range result.Rows {
		assert.Contains(t, []int64{0, 1, 2}, row[0].IntVal)
	}
}

func TestAudit_CaseWhen_Else(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT CASE WHEN 1=2 THEN 'yes' ELSE 'no' END FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "no", result.Rows[0][0].StrVal)
}

func TestAudit_CaseWhen_NoElseReturnsNull(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT CASE WHEN 1=2 THEN 'yes' END FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "CASE without ELSE must return NULL when no WHEN matches")
}

// ─────────────────────────────────────────────────────────────────────────────
// Scalar subqueries
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_ScalarSubquery(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id, (SELECT COUNT(*) FROM orders WHERE customer_id = customers.id)
		FROM customers ORDER BY id LIMIT 5
	`)
	require.Len(t, result.Rows, 5)
	for _, row := range result.Rows {
		assert.GreaterOrEqual(t, row[1].IntVal, int64(0))
	}
}

func TestAudit_ScalarSubquery_NullWhenEmpty(t *testing.T) {
	db := newTestDB(t)
	// Subquery returns no rows → scalar result is NULL
	result := db.run(t, `
		SELECT (SELECT id FROM customers WHERE id = 999999)
		FROM customers LIMIT 1
	`)
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "scalar subquery with no rows must return NULL")
}

// ─────────────────────────────────────────────────────────────────────────────
// Built-in scalar functions
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Coalesce(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT COALESCE(NULL, NULL, 42) FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(42), result.Rows[0][0].IntVal)
}

func TestAudit_Coalesce_AllNull(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT COALESCE(NULL, NULL) FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "COALESCE of all NULLs must return NULL")
}

func TestAudit_Nullif(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT NULLIF(1, 1), NULLIF(1, 2) FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "NULLIF(1,1) must return NULL")
	assert.Equal(t, int64(1), result.Rows[0][1].IntVal, "NULLIF(1,2) must return 1")
}

func TestAudit_Upper_Lower(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT UPPER('hello'), LOWER('WORLD') FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "HELLO", result.Rows[0][0].StrVal)
	assert.Equal(t, "world", result.Rows[0][1].StrVal)
}

func TestAudit_Length(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT LENGTH('abc'), LENGTH('') FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(3), result.Rows[0][0].IntVal)
	assert.Equal(t, int64(0), result.Rows[0][1].IntVal)
}

func TestAudit_Abs(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT ABS(-5), ABS(3), ABS(-3.14) FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(5), result.Rows[0][0].IntVal)
	assert.Equal(t, int64(3), result.Rows[0][1].IntVal)
	assert.InDelta(t, 3.14, result.Rows[0][2].FloatVal, 0.001)
}

func TestAudit_StringConcat(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 'hello' || ' ' || 'world' FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "hello world", result.Rows[0][0].StrVal)
}

func TestAudit_StringConcat_WithNull(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 'hello' || NULL FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "NULL concatenation must return NULL")
}

// ─────────────────────────────────────────────────────────────────────────────
// Multi-aggregate in one SELECT
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_MultiAggregate(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT COUNT(*), SUM(amount), AVG(amount), MIN(amount), MAX(amount) FROM orders`)
	require.Len(t, result.Rows, 1)
	row := result.Rows[0]
	assert.Equal(t, int64(1000), row[0].IntVal, "COUNT(*)")
	assert.Equal(t, catalog.TypeFloat, row[1].Type, "SUM returns float")
	assert.Greater(t, row[1].FloatVal, 0.0, "SUM > 0")
	assert.Equal(t, catalog.TypeFloat, row[2].Type, "AVG returns float")
	assert.Greater(t, row[2].FloatVal, 0.0, "AVG > 0")
	// MIN < AVG < MAX
	assert.Less(t, row[3].FloatVal, row[2].FloatVal, "MIN < AVG")
	assert.Greater(t, row[4].FloatVal, row[2].FloatVal, "MAX > AVG")
}

func TestAudit_AvgIsCorrect(t *testing.T) {
	db := newTestDB(t)
	sum := db.run(t, "SELECT SUM(amount) FROM orders").Rows[0][0].FloatVal
	count := db.run(t, "SELECT COUNT(*) FROM orders").Rows[0][0].IntVal
	avg := db.run(t, "SELECT AVG(amount) FROM orders").Rows[0][0].FloatVal
	expected := sum / float64(count)
	assert.InDelta(t, expected, avg, 0.001, "AVG must equal SUM/COUNT")
}

func TestAudit_MinMaxTypes(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT MIN(id), MAX(id) FROM customers`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(1), result.Rows[0][0].IntVal, "MIN(id) for customers 1-100 = 1")
	assert.Equal(t, int64(100), result.Rows[0][1].IntVal, "MAX(id) for customers 1-100 = 100")
}

func TestAudit_CountOnEmptyTable(t *testing.T) {
	db := newTestDB(t)
	// No orders for customer 9999 (doesn't exist)
	result := db.run(t, "SELECT COUNT(*) FROM orders WHERE customer_id = 9999")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(0), result.Rows[0][0].IntVal)
}

func TestAudit_SumOnEmptyResult(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT SUM(amount) FROM orders WHERE customer_id = 9999")
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "SUM over empty set must return NULL")
}

// ─────────────────────────────────────────────────────────────────────────────
// GROUP BY + HAVING (complex)
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_GroupBy_MultiColumn(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT status, COUNT(*) FROM orders
		GROUP BY status
		HAVING COUNT(*) > 0
		ORDER BY status
	`)
	require.Greater(t, len(result.Rows), 0)
	for _, row := range result.Rows {
		assert.Greater(t, row[1].IntVal, int64(0))
	}
}

func TestAudit_Having_SumFilter(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT customer_id, SUM(amount)
		FROM orders
		GROUP BY customer_id
		HAVING SUM(amount) > 1000
	`)
	for _, row := range result.Rows {
		assert.Greater(t, row[1].FloatVal, float64(1000), "HAVING filter violated")
	}
}

func TestAudit_GroupBy_OrderBy_Aggregate(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT status, COUNT(*) as cnt
		FROM orders
		GROUP BY status
		ORDER BY cnt DESC
		LIMIT 1
	`)
	require.Len(t, result.Rows, 1)
	// shipped is the most common (60% probability) — just verify it's a valid count
	assert.Greater(t, result.Rows[0][1].IntVal, int64(0))
}

// ─────────────────────────────────────────────────────────────────────────────
// Multi-column ORDER BY
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_OrderBy_MultiColumn(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT country, id FROM customers ORDER BY country ASC, id DESC LIMIT 10
	`)
	require.Len(t, result.Rows, 10)
	// Within the same country block, ids must be descending
	prev := result.Rows[0]
	for _, row := range result.Rows[1:] {
		if row[0].StrVal == prev[0].StrVal {
			assert.LessOrEqual(t, row[1].IntVal, prev[1].IntVal, "within same country, id must be DESC")
		}
		prev = row
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LIKE case sensitivity
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Like_CaseSensitive(t *testing.T) {
	db := newTestDB(t)
	// Seed names are "FirstName LastName" — uppercase first letter
	upper := db.run(t, "SELECT COUNT(*) FROM customers WHERE name LIKE 'Alice%'").Rows[0][0].IntVal
	lower := db.run(t, "SELECT COUNT(*) FROM customers WHERE name LIKE 'alice%'").Rows[0][0].IntVal
	// LIKE is case-sensitive after the fix; lowercase pattern must return 0
	assert.Equal(t, int64(0), lower, "lowercase LIKE pattern must not match uppercase names")
	assert.GreaterOrEqual(t, upper, int64(0))
}

func TestAudit_Like_Percent(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE email LIKE '%@example.com'")
	require.Len(t, result.Rows, 1)
	// All emails end in @example.com
	assert.Equal(t, int64(100), result.Rows[0][0].IntVal)
}

func TestAudit_Like_Underscore(t *testing.T) {
	db := newTestDB(t)
	// email pattern: firstname.lastnameN@example.com — use _ as single-char wildcard
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE name LIKE '_____%'") // at least 5 chars
	require.Len(t, result.Rows, 1)
	// All names are "FirstName LastName" so all have ≥5 chars
	assert.Equal(t, int64(100), result.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// IS NULL / IS NOT NULL
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_IsNull_CoalesceJoin(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (997, 'Orphan', 'orphan@test.com', 'US', '2024-01-01')`)

	// LEFT JOIN; then filter for rows where right side is NULL
	result := db.run(t, `
		SELECT c.id FROM customers c
		LEFT JOIN orders o ON c.id = o.customer_id
		WHERE o.id IS NULL AND c.id = 997
	`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(997), result.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// Arithmetic edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Arithmetic_DivisionByZero(t *testing.T) {
	db := newTestDB(t)
	// Division by zero should produce a runtime error
	_, err := runSQL(db, "SELECT 1 / 0 FROM customers LIMIT 1")
	assert.Error(t, err, "division by zero should return an error")
}

func TestAudit_Arithmetic_UnaryMinus(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT -id FROM customers ORDER BY id LIMIT 3")
	require.Len(t, result.Rows, 3)
	assert.Equal(t, int64(-1), result.Rows[0][0].IntVal)
	assert.Equal(t, int64(-2), result.Rows[1][0].IntVal)
}

func TestAudit_Arithmetic_Mixed_FloatInt(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT 3 + 1.5 FROM customers LIMIT 1")
	require.Len(t, result.Rows, 1)
	assert.InDelta(t, 4.5, result.Rows[0][0].FloatVal, 0.001)
}

func TestAudit_Arithmetic_Mod(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id % 2 = 0")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(50), result.Rows[0][0].IntVal) // IDs 2,4,...,100
}

// ─────────────────────────────────────────────────────────────────────────────
// NULL propagation in expressions
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Null_InArithmetic(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT NULL + 1 FROM customers LIMIT 1")
	require.Len(t, result.Rows, 1)
	assert.True(t, result.Rows[0][0].IsNull, "NULL + 1 must be NULL")
}

func TestAudit_Null_InComparison(t *testing.T) {
	db := newTestDB(t)
	// NULL = NULL is NULL (not TRUE), so no rows should pass
	result := db.run(t, "SELECT COUNT(*) FROM orders WHERE NULL = NULL")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(0), result.Rows[0][0].IntVal, "NULL = NULL is NULL, not TRUE")
}

func TestAudit_Null_AndFalse(t *testing.T) {
	db := newTestDB(t)
	// NULL AND FALSE = FALSE (three-valued logic)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE NULL AND 1=2")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(0), result.Rows[0][0].IntVal)
}

func TestAudit_Null_OrTrue(t *testing.T) {
	db := newTestDB(t)
	// NULL OR TRUE = TRUE
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE NULL OR 1=1")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(100), result.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// IN / NOT IN edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_In_List(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id IN (1, 2, 3, 4, 5)")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(5), result.Rows[0][0].IntVal)
}

func TestAudit_NotIn_NoNull(t *testing.T) {
	db := newTestDB(t)
	// Without NULLs, NOT IN works normally
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id NOT IN (1, 2, 3)")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(97), result.Rows[0][0].IntVal)
}

func TestAudit_NotIn_WithNull_ReturnsZero(t *testing.T) {
	db := newTestDB(t)
	// x NOT IN (v, NULL) → NULL for all x not in the list → WHERE = NULL → 0 rows
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id NOT IN (1, NULL)")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(0), result.Rows[0][0].IntVal,
		"NOT IN with NULL must return 0 rows (SQL three-valued logic)")
}

func TestAudit_In_WithNull_MatchingValue(t *testing.T) {
	db := newTestDB(t)
	// x IN (x, NULL) → TRUE (found a match before encountering NULL)
	result := db.run(t, "SELECT COUNT(*) FROM customers WHERE id IN (1, NULL)")
	require.Len(t, result.Rows, 1)
	// Only id=1 gives TRUE; all others give NULL (not TRUE) → 0 rows pass WHERE
	assert.Equal(t, int64(1), result.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// Subquery correctness
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_InSubquery_Correctness(t *testing.T) {
	db := newTestDB(t)
	// Customers who have shipped orders
	result := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')
	`)
	require.Len(t, result.Rows, 1)
	count := result.Rows[0][0].IntVal
	assert.Greater(t, count, int64(0))
	assert.LessOrEqual(t, count, int64(100))
}

func TestAudit_NotInSubquery_Basic(t *testing.T) {
	db := newTestDB(t)
	// Total = in + notIn (since subquery has no NULLs)
	inCount := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE id IN (SELECT customer_id FROM orders WHERE status = 'shipped')
	`).Rows[0][0].IntVal
	notInCount := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE id NOT IN (SELECT customer_id FROM orders WHERE status = 'shipped')
	`).Rows[0][0].IntVal
	assert.Equal(t, int64(100), inCount+notInCount,
		"IN and NOT IN (no NULLs) must partition the 100 customers")
}

func TestAudit_ExistsSubquery(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT COUNT(*) FROM customers
		WHERE EXISTS (SELECT 1 FROM orders WHERE orders.customer_id = customers.id AND status = 'cancelled')
	`)
	require.Len(t, result.Rows, 1)
	assert.GreaterOrEqual(t, result.Rows[0][0].IntVal, int64(0))
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser features: SQL comments and quoted identifiers
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Parser_LineComment(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id -- this is a comment
		FROM customers
		LIMIT 1
	`)
	require.Len(t, result.Rows, 1)
}

func TestAudit_Parser_BlockComment(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT id /* block comment */ FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
}

func TestAudit_Parser_QuotedIdentifier(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT "id" FROM customers LIMIT 1`)
	require.Len(t, result.Rows, 1)
}

// ─────────────────────────────────────────────────────────────────────────────
// SELECT without FROM (constant expressions, now with WHERE)
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_SelectConstant_ArithmeticAndFunctions(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 2 * 3 + 1, UPPER('hello'), LENGTH('world')`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(7), result.Rows[0][0].IntVal)
	assert.Equal(t, "HELLO", result.Rows[0][1].StrVal)
	assert.Equal(t, int64(5), result.Rows[0][2].IntVal)
}

func TestAudit_SelectWithoutFrom_Where_True(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 42 WHERE 1 = 1`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(42), result.Rows[0][0].IntVal)
}

func TestAudit_SelectWithoutFrom_Where_False(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `SELECT 42 WHERE 1 = 2`)
	assert.Len(t, result.Rows, 0)
}

func TestAudit_SelectCount_WithoutFrom(t *testing.T) {
	db := newTestDB(t)
	// SELECT COUNT(*) with no FROM — counts the single implicit row
	result := db.run(t, `SELECT COUNT(*)`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(1), result.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// CREATE TABLE + INSERT + SELECT (DDL round-trip)
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_CreateTable_InsertSelect(t *testing.T) {
	db := newTestDB(t)

	// Create a fresh table (via API-style handler tests)
	// Since executor_test only drives the executor, we test INSERT + SELECT directly.
	db.run(t, `INSERT INTO products (id, name, category, price) VALUES (5001, 'NewProd', 'test', 9.99)`)
	result := db.run(t, `SELECT id, name, price FROM products WHERE id = 5001`)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(5001), result.Rows[0][0].IntVal)
	assert.Equal(t, "NewProd", result.Rows[0][1].StrVal)
	assert.InDelta(t, 9.99, result.Rows[0][2].FloatVal, 0.001)
}

func TestAudit_Insert_CaseInsensitiveColumns(t *testing.T) {
	db := newTestDB(t)
	// Column names in INSERT should be case-insensitive
	db.run(t, `INSERT INTO products (ID, NAME, CATEGORY, PRICE) VALUES (5002, 'CaseTest', 'misc', 1.00)`)
	result := db.run(t, `SELECT id FROM products WHERE id = 5002`)
	require.Len(t, result.Rows, 1)
}

// ─────────────────────────────────────────────────────────────────────────────
// Three-table join correctness
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_ThreeWayJoin_Correctness(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT c.id, p.id, o.amount
		FROM orders o
		JOIN customers c ON o.customer_id = c.id
		JOIN products p ON o.product_id = p.id
		WHERE c.id = 1
		LIMIT 5
	`)
	require.Greater(t, len(result.Rows), 0)
	for _, row := range result.Rows {
		assert.Equal(t, int64(1), row[0].IntVal, "customer id must be 1")
		assert.False(t, row[1].IsNull, "product id must not be NULL")
		assert.False(t, row[2].IsNull, "amount must not be NULL")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Stats accuracy after RowsProduced fix
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_Stats_RowsProducedNotDoubled(t *testing.T) {
	db := newTestDB(t)
	ctx := newExecCtxForDB(db)
	_ = runSQLWithCtx(db, ctx, "SELECT id FROM customers LIMIT 5")
	// RowsProduced must equal the number of rows returned
	assert.Equal(t, int64(5), ctx.RowsProduced,
		"RowsProduced must equal result row count (not double-counted)")
}

// ─────────────────────────────────────────────────────────────────────────────
// UNION / INTERSECT / EXCEPT correctness
// ─────────────────────────────────────────────────────────────────────────────

func TestAudit_UnionAll_Concatenates(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id FROM customers WHERE id = 1
		UNION ALL
		SELECT id FROM customers WHERE id = 1
	`)
	assert.Len(t, result.Rows, 2, "UNION ALL keeps duplicates")
}

func TestAudit_Union_Deduplicates(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id FROM customers WHERE id = 1
		UNION
		SELECT id FROM customers WHERE id = 1
	`)
	assert.Len(t, result.Rows, 1, "UNION deduplicates")
}

func TestAudit_Intersect_CommonRows(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id FROM customers WHERE id <= 5
		INTERSECT
		SELECT id FROM customers WHERE id >= 3
	`)
	// Common rows: id 3, 4, 5
	require.Len(t, result.Rows, 3)
}

func TestAudit_Except_Subtraction(t *testing.T) {
	db := newTestDB(t)
	result := db.run(t, `
		SELECT id FROM customers WHERE id <= 5
		EXCEPT
		SELECT id FROM customers WHERE id >= 3
	`)
	// Left: 1,2,3,4,5. Minus right: 3,4,5,...  → 1,2
	require.Len(t, result.Rows, 2)
	ids := map[int64]bool{}
	for _, row := range result.Rows {
		ids[row[0].IntVal] = true
	}
	assert.True(t, ids[1])
	assert.True(t, ids[2])
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// runSQL is a convenience that allows testing error paths (like division by zero).
func runSQL(db *testDB, sql string) (*Result, error) {
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	if err != nil {
		return nil, err
	}
	a := analyzer.New(db.cat)
	if err := a.Analyze(stmt); err != nil {
		return nil, err
	}
	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	if err != nil {
		return nil, err
	}
	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)
	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	if err != nil {
		return nil, err
	}
	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	return Execute(pplan, ctx)
}

// newExecCtxForDB creates a fresh ExecContext for the test DB so we can inspect stats.
func newExecCtxForDB(db *testDB) *exectypes.ExecContext {
	return exectypes.NewExecContext(db.cat, db.store)
}

// runSQLWithCtx runs a SQL query against a pre-created context (for stats inspection).
func runSQLWithCtx(db *testDB, ctx *exectypes.ExecContext, sql string) *Result {
	p := parser.New(sql)
	stmt, _ := p.ParseStatement()
	a := analyzer.New(db.cat)
	_ = a.Analyze(stmt)
	b := logical.NewBuilder(db.cat)
	lplan, _ := b.BuildStatement(stmt)
	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)
	pb := physical.NewBuilder()
	pplan, _ := pb.Build(lplan)
	ctx.CTEs = b.GetCTEs()
	result, _ := Execute(pplan, ctx)
	return result
}
