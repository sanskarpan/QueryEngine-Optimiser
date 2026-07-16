package executor

// TestProbe_BUG001_NotLike probes BUG-001: NOT LIKE is not handled as a
// postfix negated operator. The parser only special-cases NOT IN and NOT
// BETWEEN at parser.go:588. When `col NOT LIKE '...'` is parsed:
//
//   1. parseExpression calls parseUnary, which returns ColumnRef("name")
//      (stops at the identifier before NOT).
//   2. The loop checks the NOT IN / NOT BETWEEN special case — fails because
//      peek is LIKE, not IN or BETWEEN.
//   3. NOT is NOT in the precedence map, so `prec, ok = precedence[NOT]`
//      gives ok=false, and the loop breaks.
//   4. parseExpression returns ColumnRef("name") leaving NOT LIKE '%...' unconsumed.
//   5. The parser silently ignores the rest and the WHERE clause is set to
//      just ColumnRef("name") — a column reference evaluated as a truthy
//      non-null check, not the intended LIKE negation.
//
// Expected: the query should parse to a BinaryExpr/LikeExpr with Negated=true
//           (or at minimum a UnaryExpr{NOT, BinaryExpr{name LIKE pattern}}),
//           and filter rows correctly.
// Actual:   WHERE clause becomes ColumnRef("name"), so all non-null name rows
//           pass the filter — returning the full table instead of the complement.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG001_NotLike(t *testing.T) {
	db := newTestDB(t)

	// Count rows matching LIKE directly.
	likeResult := db.run(t, "SELECT id FROM customers WHERE name LIKE '%Alice%'")
	likeCount := len(likeResult.Rows)
	require.Greater(t, likeCount, 0, "seed data must contain at least one 'Alice' name")

	// Count the full table.
	totalResult := db.run(t, "SELECT id FROM customers")
	total := len(totalResult.Rows)

	// NOT LIKE should be the complement: total - likeCount rows.
	notLikeResult := db.run(t, "SELECT id FROM customers WHERE name NOT LIKE '%Alice%'")
	notLikeCount := len(notLikeResult.Rows)

	assert.Equal(t, total-likeCount, notLikeCount,
		"NOT LIKE should return exactly the complement of LIKE; "+
			"if counts are wrong the WHERE clause was silently mis-parsed "+
			"(BUG-001: NOT LIKE not handled as postfix negated operator)")
}
