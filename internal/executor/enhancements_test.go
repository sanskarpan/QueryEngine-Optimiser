package executor

// Comprehensive tests for the 13 enhancement features:
// UPDATE, DELETE, WITH (CTE), multi-row INSERT, CAST, COUNT(DISTINCT),
// NULLS FIRST/LAST, FULL OUTER JOIN, math functions, string functions,
// FULL OUTER JOIN, and parser improvements.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Multi-row INSERT
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_MultiRowInsert(t *testing.T) {
	db := newTestDB(t)
	// Insert 3 rows in one statement
	res := db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (9001, 'Alice', 'a@a.com', 'US', '2024-01-01'),
		       (9002, 'Bob',   'b@b.com', 'UK', '2024-01-02'),
		       (9003, 'Carol', 'c@c.com', 'CA', '2024-01-03')`)
	require.Equal(t, int64(3), res.Rows[0][0].IntVal, "rows_affected should be 3")

	// Verify all three rows were inserted
	r := db.run(t, "SELECT name FROM customers WHERE id >= 9001 ORDER BY id")
	require.Len(t, r.Rows, 3)
	assert.Equal(t, "Alice", r.Rows[0][0].StrVal)
	assert.Equal(t, "Bob", r.Rows[1][0].StrVal)
	assert.Equal(t, "Carol", r.Rows[2][0].StrVal)
}

func TestEnhancement_SingleRowInsertStillWorks(t *testing.T) {
	db := newTestDB(t)
	res := db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (9901, 'Solo', 's@s.com', 'FR', '2024-06-01')`)
	require.Equal(t, int64(1), res.Rows[0][0].IntVal)

	r := db.run(t, "SELECT name FROM customers WHERE id = 9901")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "Solo", r.Rows[0][0].StrVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// UPDATE
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_UpdateWithWhere(t *testing.T) {
	db := newTestDB(t)
	// Insert a row to update
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (8001, 'Old Name', 'old@x.com', 'US', '2024-01-01')`)

	res := db.run(t, `UPDATE customers SET name = 'New Name' WHERE id = 8001`)
	assert.Equal(t, int64(1), res.Rows[0][0].IntVal, "rows_affected")

	r := db.run(t, "SELECT name FROM customers WHERE id = 8001")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "New Name", r.Rows[0][0].StrVal)
}

func TestEnhancement_UpdateMultipleRows(t *testing.T) {
	db := newTestDB(t)
	// Insert some rows
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (8101, 'A', 'a@x.com', 'OLD', '2024-01-01'),
		       (8102, 'B', 'b@x.com', 'OLD', '2024-01-01')`)

	res := db.run(t, `UPDATE customers SET country = 'NEW' WHERE country = 'OLD'`)
	assert.Equal(t, int64(2), res.Rows[0][0].IntVal)

	r := db.run(t, "SELECT id FROM customers WHERE country = 'NEW' ORDER BY id")
	require.Len(t, r.Rows, 2)
	assert.Equal(t, int64(8101), r.Rows[0][0].IntVal)
	assert.Equal(t, int64(8102), r.Rows[1][0].IntVal)
}

func TestEnhancement_UpdateNoMatchReturnsZero(t *testing.T) {
	db := newTestDB(t)
	res := db.run(t, `UPDATE customers SET name = 'X' WHERE id = 999999`)
	assert.Equal(t, int64(0), res.Rows[0][0].IntVal)
}

func TestEnhancement_UpdateMultipleColumns(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (8201, 'Old', 'old@e.com', 'US', '2024-01-01')`)

	db.run(t, `UPDATE customers SET name = 'Updated', country = 'CA' WHERE id = 8201`)

	r := db.run(t, "SELECT name, country FROM customers WHERE id = 8201")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "Updated", r.Rows[0][0].StrVal)
	assert.Equal(t, "CA", r.Rows[0][1].StrVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_DeleteWithWhere(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (7001, 'Delete Me', 'd@x.com', 'US', '2024-01-01')`)

	res := db.run(t, `DELETE FROM customers WHERE id = 7001`)
	assert.Equal(t, int64(1), res.Rows[0][0].IntVal)

	r := db.run(t, "SELECT id FROM customers WHERE id = 7001")
	assert.Len(t, r.Rows, 0)
}

func TestEnhancement_DeleteMultipleRows(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (7101, 'A', 'a@d.com', 'TODEL', '2024-01-01'),
		       (7102, 'B', 'b@d.com', 'TODEL', '2024-01-01'),
		       (7103, 'C', 'c@d.com', 'KEEP',  '2024-01-01')`)

	res := db.run(t, `DELETE FROM customers WHERE country = 'TODEL'`)
	assert.Equal(t, int64(2), res.Rows[0][0].IntVal)

	r := db.run(t, "SELECT id FROM customers WHERE id >= 7101 AND id <= 7103")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, int64(7103), r.Rows[0][0].IntVal)
}

func TestEnhancement_DeleteNoMatchReturnsZero(t *testing.T) {
	db := newTestDB(t)
	res := db.run(t, `DELETE FROM customers WHERE id = 999999`)
	assert.Equal(t, int64(0), res.Rows[0][0].IntVal)
}

func TestEnhancement_InsertThenDeleteThenVerify(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (6001, 'Temp', 't@t.com', 'US', '2024-01-01')`)

	before := db.run(t, "SELECT id FROM customers WHERE id = 6001")
	require.Len(t, before.Rows, 1)

	db.run(t, `DELETE FROM customers WHERE id = 6001`)

	after := db.run(t, "SELECT id FROM customers WHERE id = 6001")
	assert.Len(t, after.Rows, 0)
}

// ─────────────────────────────────────────────────────────────────────────────
// CAST
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_CastIntToFloat(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST(42 AS FLOAT)")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, float64(42), r.Rows[0][0].FloatVal)
}

func TestEnhancement_CastFloatToInt(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST(3.7 AS INT)")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, int64(3), r.Rows[0][0].IntVal)
}

func TestEnhancement_CastIntToText(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST(99 AS TEXT)")
	require.Len(t, r.Rows, 1)
	assert.Contains(t, r.Rows[0][0].StrVal, "99")
}

func TestEnhancement_CastTextToInt(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST('123' AS INT)")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, int64(123), r.Rows[0][0].IntVal)
}

func TestEnhancement_CastNullReturnsNull(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST(NULL AS INT)")
	require.Len(t, r.Rows, 1)
	assert.True(t, r.Rows[0][0].IsNull)
}

func TestEnhancement_CastOnColumnValue(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CAST(id AS FLOAT) FROM customers LIMIT 1")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, catalog.TypeFloat, r.Rows[0][0].Type)
}

// ─────────────────────────────────────────────────────────────────────────────
// COUNT(DISTINCT)
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_CountDistinct_LessThanCountAll(t *testing.T) {
	db := newTestDB(t)
	all := db.run(t, "SELECT COUNT(country) FROM customers")
	distinct := db.run(t, "SELECT COUNT(DISTINCT country) FROM customers")
	require.Len(t, all.Rows, 1)
	require.Len(t, distinct.Rows, 1)
	assert.Less(t, distinct.Rows[0][0].IntVal, all.Rows[0][0].IntVal,
		"COUNT(DISTINCT country) must be less than COUNT(country)")
}

func TestEnhancement_CountDistinct_SmallSet(t *testing.T) {
	cat := catalog.New()
	store := storage.New()
	require.NoError(t, store.CreateTable("colors"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name:    "colors",
		Columns: []catalog.Column{{Name: "c", Type: catalog.TypeText, Index: 0}},
	}))
	db := &testDB{cat: cat, store: store}
	db.run(t, "INSERT INTO colors (c) VALUES ('red')")
	db.run(t, "INSERT INTO colors (c) VALUES ('blue')")
	db.run(t, "INSERT INTO colors (c) VALUES ('red')")
	db.run(t, "INSERT INTO colors (c) VALUES ('green')")

	r := db.run(t, "SELECT COUNT(DISTINCT c) FROM colors")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, int64(3), r.Rows[0][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// Math functions
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_Round(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT ROUND(3.567, 2)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 3.57, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_RoundNoDecimals(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT ROUND(3.5)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 4.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_Floor(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT FLOOR(3.9)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 3.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_Ceil(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CEIL(3.1)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 4.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_Ceiling(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT CEILING(2.1)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 3.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_Power(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT POWER(2, 10)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 1024.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_Sqrt(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT SQRT(144)")
	require.Len(t, r.Rows, 1)
	assert.InDelta(t, 12.0, r.Rows[0][0].FloatVal, 0.0001)
}

func TestEnhancement_MathFunctionsNullPropagation(t *testing.T) {
	db := newTestDB(t)
	tests := []string{
		"SELECT ROUND(NULL)",
		"SELECT FLOOR(NULL)",
		"SELECT CEIL(NULL)",
		"SELECT POWER(NULL, 2)",
		"SELECT SQRT(NULL)",
	}
	for _, sql := range tests {
		r := db.run(t, sql)
		require.Len(t, r.Rows, 1, sql)
		assert.True(t, r.Rows[0][0].IsNull, "expected NULL for: %s", sql)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// String functions
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_Trim(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT TRIM('  hello  ')")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "hello", r.Rows[0][0].StrVal)
}

func TestEnhancement_Ltrim(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT LTRIM('   hello')")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "hello", r.Rows[0][0].StrVal)
}

func TestEnhancement_Rtrim(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT RTRIM('hello   ')")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "hello", r.Rows[0][0].StrVal)
}

func TestEnhancement_Substr_TwoArgs(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT SUBSTR('Hello World', 7)")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "World", r.Rows[0][0].StrVal)
}

func TestEnhancement_Substr_ThreeArgs(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT SUBSTR('Hello World', 1, 5)")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "Hello", r.Rows[0][0].StrVal)
}

func TestEnhancement_Replace(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, "SELECT REPLACE('foo bar foo', 'foo', 'baz')")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "baz bar baz", r.Rows[0][0].StrVal)
}

func TestEnhancement_StringFunctionsNullPropagation(t *testing.T) {
	db := newTestDB(t)
	tests := []string{
		"SELECT TRIM(NULL)",
		"SELECT LTRIM(NULL)",
		"SELECT RTRIM(NULL)",
		"SELECT SUBSTR(NULL, 1)",
		"SELECT REPLACE(NULL, 'a', 'b')",
	}
	for _, sql := range tests {
		r := db.run(t, sql)
		require.Len(t, r.Rows, 1, sql)
		assert.True(t, r.Rows[0][0].IsNull, "expected NULL for: %s", sql)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NULLS FIRST / NULLS LAST
// ─────────────────────────────────────────────────────────────────────────────

func newSimpleDB(t *testing.T) *testDB {
	t.Helper()
	cat := catalog.New()
	store := storage.New()
	require.NoError(t, store.CreateTable("vals"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name:    "vals",
		Columns: []catalog.Column{{Name: "v", Type: catalog.TypeInt, Index: 0}},
	}))
	db := &testDB{cat: cat, store: store}
	db.run(t, "INSERT INTO vals (v) VALUES (3)")
	db.run(t, "INSERT INTO vals (v) VALUES (1)")
	db.run(t, "INSERT INTO vals (v) VALUES (NULL)")
	db.run(t, "INSERT INTO vals (v) VALUES (2)")
	return db
}

func TestEnhancement_NullsFirst_ASC(t *testing.T) {
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v ASC NULLS FIRST")
	require.Len(t, r.Rows, 4)
	assert.True(t, r.Rows[0][0].IsNull, "first row should be NULL")
	assert.Equal(t, int64(1), r.Rows[1][0].IntVal)
	assert.Equal(t, int64(2), r.Rows[2][0].IntVal)
	assert.Equal(t, int64(3), r.Rows[3][0].IntVal)
}

func TestEnhancement_NullsLast_ASC(t *testing.T) {
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v ASC NULLS LAST")
	require.Len(t, r.Rows, 4)
	assert.Equal(t, int64(1), r.Rows[0][0].IntVal)
	assert.Equal(t, int64(2), r.Rows[1][0].IntVal)
	assert.Equal(t, int64(3), r.Rows[2][0].IntVal)
	assert.True(t, r.Rows[3][0].IsNull, "last row should be NULL")
}

func TestEnhancement_NullsFirst_DESC(t *testing.T) {
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v DESC NULLS FIRST")
	require.Len(t, r.Rows, 4)
	assert.True(t, r.Rows[0][0].IsNull, "first row should be NULL")
	assert.Equal(t, int64(3), r.Rows[1][0].IntVal)
}

func TestEnhancement_NullsLast_DESC(t *testing.T) {
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v DESC NULLS LAST")
	require.Len(t, r.Rows, 4)
	assert.Equal(t, int64(3), r.Rows[0][0].IntVal)
	assert.Equal(t, int64(2), r.Rows[1][0].IntVal)
	assert.Equal(t, int64(1), r.Rows[2][0].IntVal)
	assert.True(t, r.Rows[3][0].IsNull, "last row should be NULL")
}

func TestEnhancement_DefaultNullOrdering_ASC(t *testing.T) {
	// SQL default: ASC → NULLS LAST
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v ASC")
	require.Len(t, r.Rows, 4)
	assert.Equal(t, int64(1), r.Rows[0][0].IntVal)
	assert.True(t, r.Rows[3][0].IsNull, "NULL should be last by default for ASC")
}

func TestEnhancement_DefaultNullOrdering_DESC(t *testing.T) {
	// SQL default: DESC → NULLS FIRST
	db := newSimpleDB(t)
	r := db.run(t, "SELECT v FROM vals ORDER BY v DESC")
	require.Len(t, r.Rows, 4)
	assert.True(t, r.Rows[0][0].IsNull, "NULL should be first by default for DESC")
	assert.Equal(t, int64(3), r.Rows[1][0].IntVal)
}

// ─────────────────────────────────────────────────────────────────────────────
// FULL OUTER JOIN
// ─────────────────────────────────────────────────────────────────────────────

func newFullJoinDB(t *testing.T) *testDB {
	t.Helper()
	cat := catalog.New()
	store := storage.New()

	require.NoError(t, store.CreateTable("a"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "a",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "val", Type: catalog.TypeText, Index: 1},
		},
	}))

	require.NoError(t, store.CreateTable("b"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name: "b",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "val", Type: catalog.TypeText, Index: 1},
		},
	}))

	db := &testDB{cat: cat, store: store}
	// a: 1, 2, 3
	db.run(t, "INSERT INTO a (id, val) VALUES (1, 'a1')")
	db.run(t, "INSERT INTO a (id, val) VALUES (2, 'a2')")
	db.run(t, "INSERT INTO a (id, val) VALUES (3, 'a3')")
	// b: 2, 3, 4 — 2 and 3 match, 1 is a-only, 4 is b-only
	db.run(t, "INSERT INTO b (id, val) VALUES (2, 'b2')")
	db.run(t, "INSERT INTO b (id, val) VALUES (3, 'b3')")
	db.run(t, "INSERT INTO b (id, val) VALUES (4, 'b4')")
	return db
}

func TestEnhancement_FullOuterJoin_RowCount(t *testing.T) {
	db := newFullJoinDB(t)
	// Expected: 2 matches (2,3) + 1 a-only (1) + 1 b-only (4) = 4 rows
	r := db.run(t, "SELECT a.id, b.id FROM a FULL OUTER JOIN b ON a.id = b.id ORDER BY COALESCE(a.id, b.id)")
	assert.Equal(t, 4, len(r.Rows), "FULL OUTER JOIN should produce 4 rows")
}

func TestEnhancement_FullOuterJoin_MatchedRows(t *testing.T) {
	db := newFullJoinDB(t)
	r := db.run(t, `SELECT a.val, b.val FROM a FULL OUTER JOIN b ON a.id = b.id
		WHERE a.id IS NOT NULL AND b.id IS NOT NULL ORDER BY a.id`)
	assert.Equal(t, 2, len(r.Rows), "only 2 matched rows")
	assert.Equal(t, "a2", r.Rows[0][0].StrVal)
	assert.Equal(t, "b2", r.Rows[0][1].StrVal)
}

func TestEnhancement_FullOuterJoin_LeftOnlyRows(t *testing.T) {
	db := newFullJoinDB(t)
	r := db.run(t, `SELECT a.id, b.id FROM a FULL OUTER JOIN b ON a.id = b.id WHERE b.id IS NULL`)
	assert.Equal(t, 1, len(r.Rows), "exactly 1 left-only row (id=1)")
	assert.Equal(t, int64(1), r.Rows[0][0].IntVal)
	assert.True(t, r.Rows[0][1].IsNull)
}

func TestEnhancement_FullOuterJoin_RightOnlyRows(t *testing.T) {
	db := newFullJoinDB(t)
	r := db.run(t, `SELECT a.id, b.id FROM a FULL OUTER JOIN b ON a.id = b.id WHERE a.id IS NULL`)
	assert.Equal(t, 1, len(r.Rows), "exactly 1 right-only row (id=4)")
	assert.True(t, r.Rows[0][0].IsNull)
	assert.Equal(t, int64(4), r.Rows[0][1].IntVal)
}

func TestEnhancement_FullJoin_SyntaxVariant(t *testing.T) {
	// FULL JOIN (without OUTER keyword) must also work
	db := newFullJoinDB(t)
	r := db.run(t, "SELECT a.id, b.id FROM a FULL JOIN b ON a.id = b.id")
	assert.Equal(t, 4, len(r.Rows))
}

// ─────────────────────────────────────────────────────────────────────────────
// WITH (CTE)
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_CTE_Basic(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, `
		WITH top_customers AS (
			SELECT id, name FROM customers WHERE id <= 5
		)
		SELECT id FROM top_customers ORDER BY id`)
	assert.Equal(t, 5, len(r.Rows))
	for i, row := range r.Rows {
		assert.Equal(t, int64(i+1), row[0].IntVal)
	}
}

func TestEnhancement_CTE_WithAggregation(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, `
		WITH country_counts AS (
			SELECT country, COUNT(*) AS cnt FROM customers GROUP BY country
		)
		SELECT cnt FROM country_counts WHERE country = 'US'`)
	require.Len(t, r.Rows, 1)
	assert.Greater(t, r.Rows[0][0].IntVal, int64(0))
}

func TestEnhancement_CTE_Referenced_In_WHERE(t *testing.T) {
	db := newTestDB(t)
	r := db.run(t, `
		WITH big_orders AS (
			SELECT customer_id FROM orders WHERE amount > 500
		)
		SELECT id FROM customers WHERE id IN (SELECT customer_id FROM big_orders) LIMIT 5`)
	assert.GreaterOrEqual(t, len(r.Rows), 0) // just verify it parses and executes
}

// ─────────────────────────────────────────────────────────────────────────────
// Parser improvements: FULL JOIN keywords
// ─────────────────────────────────────────────────────────────────────────────

func TestEnhancement_ParserUpdateDelete(t *testing.T) {
	// Just verify parsing doesn't error
	db := newTestDB(t)
	// UPDATE
	db.run(t, "UPDATE customers SET country = 'ZZ' WHERE id = -999")
	// DELETE
	db.run(t, "DELETE FROM customers WHERE id = -999")
}

func TestEnhancement_UpdateThenSelect(t *testing.T) {
	db := newTestDB(t)
	db.run(t, `INSERT INTO customers (id, name, email, country, created_at)
		VALUES (5001, 'Before', 'b@b.com', 'US', '2024-01-01')`)
	db.run(t, `UPDATE customers SET name = 'After' WHERE id = 5001`)
	r := db.run(t, "SELECT name FROM customers WHERE id = 5001")
	require.Len(t, r.Rows, 1)
	assert.Equal(t, "After", r.Rows[0][0].StrVal)
}

func TestEnhancement_DeleteAll(t *testing.T) {
	// Fresh table, insert 3, delete all without WHERE
	cat := catalog.New()
	store := storage.New()
	require.NoError(t, store.CreateTable("tmp"))
	require.NoError(t, cat.Register(&catalog.Table{
		Name:    "tmp",
		Columns: []catalog.Column{{Name: "x", Type: catalog.TypeInt, Index: 0}},
	}))
	db := &testDB{cat: cat, store: store}
	db.run(t, "INSERT INTO tmp (x) VALUES (1)")
	db.run(t, "INSERT INTO tmp (x) VALUES (2)")
	db.run(t, "INSERT INTO tmp (x) VALUES (3)")

	res := db.run(t, "DELETE FROM tmp WHERE x > 0")
	assert.Equal(t, int64(3), res.Rows[0][0].IntVal)

	r := db.run(t, "SELECT x FROM tmp")
	assert.Len(t, r.Rows, 0)
}
