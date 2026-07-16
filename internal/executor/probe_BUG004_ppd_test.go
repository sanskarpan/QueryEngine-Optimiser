package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG004_ProjectionPushdownJoinColumnPruning probes BUG-004:
// Projection pushdown in projection_pushdown.go may incorrectly prune columns
// that are needed for join conditions when table aliases are involved.
//
// The core claim is that neededFromSchema checks required[col.Name] where
// col.Name is the qualified form 'alias.column'. If a column appears in the
// ON clause only via a bare reference (no table qualifier), neededFromSchema
// may prune it, causing the join to produce wrong or empty results.
//
// We use the existing seed tables: customers c JOIN orders o ON c.id = o.customer_id
// and select only one column (c.name). The optimizer should not prune o.customer_id
// from the orders scan even though it is not selected; it is needed for the join.
func TestProbe_BUG004_ProjectionPushdownJoinColumnPruning(t *testing.T) {
	db := newTestDB(t)

	// Baseline: inner join, selecting only c.name.
	// The join key o.customer_id must NOT be pruned by projection pushdown;
	// if it is, the join will produce incorrect (zero or wrong) rows.
	result := db.run(t,
		`SELECT c.name
		 FROM customers c
		 JOIN orders o ON c.id = o.customer_id
		 LIMIT 5`)

	require.Greater(t, len(result.Rows), 0,
		"BUG-004: expected rows from join but got none — join key may have been pruned by projection pushdown")
	assert.Len(t, result.Columns, 1, "expected exactly 1 column (c.name)")

	// Every returned name must be non-empty (a valid customer name from seed data).
	for i, row := range result.Rows {
		require.Len(t, row, 1, "row %d: expected 1 value", i)
		assert.NotEmpty(t, row[0].StrVal,
			"BUG-004: row %d: c.name is empty — projection pushdown may have pruned the wrong column", i)
	}

	// Stricter test: count rows with explicit join key in SELECT to compare.
	// If the join key is pruned on the right side, the inner join yields 0 rows.
	// If projection pushdown is correct, both queries yield the same count.
	withKey := db.run(t,
		`SELECT c.name, o.customer_id
		 FROM customers c
		 JOIN orders o ON c.id = o.customer_id
		 LIMIT 5`)

	assert.Equal(t, len(result.Rows), len(withKey.Rows),
		"BUG-004: row count differs when join key is in SELECT vs not — "+
			"projection pushdown may be pruning o.customer_id when not selected")

	// Additional probe: verify the same result without LIMIT so we get the full count.
	fullResult := db.run(t,
		`SELECT c.name
		 FROM customers c
		 JOIN orders o ON c.id = o.customer_id`)

	fullWithKey := db.run(t,
		`SELECT c.name, o.customer_id
		 FROM customers c
		 JOIN orders o ON c.id = o.customer_id`)

	assert.Equal(t, len(fullResult.Rows), len(fullWithKey.Rows),
		"BUG-004: full join row count differs when join key is in SELECT vs not — "+
			"expected %d rows (with key) but got %d rows (without key in SELECT)",
		len(fullWithKey.Rows), len(fullResult.Rows))

	t.Logf("rows without join key in SELECT: %d", len(fullResult.Rows))
	t.Logf("rows with join key in SELECT: %d", len(fullWithKey.Rows))

	// Also test: LEFT join key column on left side must not be pruned even when only
	// a right-side column is selected.
	rightOnlyResult := db.run(t,
		`SELECT o.amount
		 FROM customers c
		 JOIN orders o ON c.id = o.customer_id
		 LIMIT 5`)

	require.Greater(t, len(rightOnlyResult.Rows), 0,
		"BUG-004: selecting only o.amount from join gave no rows — c.id may have been pruned")
}
