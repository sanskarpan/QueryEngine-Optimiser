package executor

// TestProbe_BUG002_PredicatePushdownUnresolvedColumn probes BUG-002:
//
// In predicate_pushdown.go, canSubsume (constant_folding.go:394-401) calls
// predicateTables which collects table names only from ColumnRef.ResolvedTable.
// When a ColumnRef has no ResolvedTable set (unqualified column reference, e.g.
// WHERE id = 5), predicateTables returns an empty map. canSubsume then iterates
// over an empty set and returns true unconditionally. This means any unqualified
// predicate is treated as pushable to whichever side is checked first — it is
// pushed to the left child even if the column belongs to the right child,
// silently producing wrong results.
//
// Repro SQL:
//   SELECT * FROM orders o JOIN customers c ON o.customer_id = c.id WHERE name = 'Alice'
//   -- 'name' unqualified, may be pushed to 'orders' instead of 'customers'
//
// Expected: a predicate with unresolved table references should remain above the
// join (in 'remaining') rather than being pushed to an arbitrary side.
// Concretely, the result of the unqualified-filter query must equal the
// result of the equivalent qualified-filter query.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG002_PredicatePushdownUnresolvedColumn(t *testing.T) {
	db := newTestDB(t)

	// Establish a baseline: how many customers have 'Alice' in their name.
	aliceCustomers := db.run(t, "SELECT id FROM customers WHERE name LIKE '%Alice%'")
	aliceCount := len(aliceCustomers.Rows)
	t.Logf("customers with 'Alice' in name: %d", aliceCount)
	require.Greater(t, aliceCount, 0, "seed data must include at least one customer named Alice")

	// Run the join query with a QUALIFIED predicate on c.name — this is the known-correct baseline.
	// The predicate 'c.name LIKE ...' has Table="c" and after analysis ResolvedTable="c".
	// predicateTables returns {"c": true}, canSubsume correctly identifies it belongs to the right side.
	qualifiedResult := db.run(t,
		`SELECT o.id, c.id, c.name
		 FROM orders o
		 JOIN customers c ON o.customer_id = c.id
		 WHERE c.name LIKE '%Alice%'`)
	t.Logf("qualified filter (c.name) row count: %d", len(qualifiedResult.Rows))
	require.Greater(t, len(qualifiedResult.Rows), 0, "join with qualified filter should return rows")

	// Run the same join query with an UNQUALIFIED predicate on name.
	// 'name' is a column that exists only in 'customers' (not in 'orders').
	// After analysis by the analyzer, the ColumnRef for 'name' gets ResolvedTable = "c".
	// Therefore predicateTables returns {"c": true} and canSubsume correctly assigns it
	// to the right (customers) side.
	//
	// If the bug were to fire (ResolvedTable remained empty → predicateTables returns {}
	// → canSubsume returns true for any side), the predicate would be pushed to 'orders'
	// (left side), which has no 'name' column, causing either wrong results or a runtime error.
	unqualifiedResult := db.run(t,
		`SELECT o.id, c.id, c.name
		 FROM orders o
		 JOIN customers c ON o.customer_id = c.id
		 WHERE name LIKE '%Alice%'`)
	t.Logf("unqualified filter (name) row count: %d", len(unqualifiedResult.Rows))

	// Primary assertion: qualified and unqualified must return the same number of rows.
	// If the predicate were pushed to the wrong side, 'orders' has no 'name' column,
	// resulting in zero rows or a runtime error (caught above by require.NoError in db.run).
	assert.Equal(t, len(qualifiedResult.Rows), len(unqualifiedResult.Rows),
		"unqualified WHERE name LIKE '%%Alice%%' must return the same row count as "+
			"qualified WHERE c.name LIKE '%%Alice%%'; a difference means the predicate "+
			"was pushed to the wrong join side (BUG-002)")

	// Secondary assertion: every returned row must have name containing 'Alice'.
	// Identify the name column index.
	nameColIdx := -1
	for i, col := range unqualifiedResult.Columns {
		if col == "c.name" || col == "name" {
			nameColIdx = i
			break
		}
	}
	t.Logf("result columns: %v", unqualifiedResult.Columns)

	if nameColIdx >= 0 {
		for i, row := range unqualifiedResult.Rows {
			name := row[nameColIdx].StrVal
			assert.Contains(t, name, "Alice",
				"row %d: name=%q should contain 'Alice'; non-Alice rows indicate filter was bypassed "+
					"(predicate pushed to wrong table)", i, name)
		}
	}
}
