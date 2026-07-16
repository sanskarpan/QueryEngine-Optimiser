package executor

// TestProbe_BUG004_DropColumnHeapMisalignment probes BUG-004:
// ALTER TABLE DROP COLUMN removes the column from the catalog schema
// (catalog.go:DropColumn) but does NOT remove the corresponding value from
// existing storage rows in the HeapTable. After DROP COLUMN, each heap row
// still has the old number of values, so column indexes are permanently
// misaligned between schema and data: reads will map values to wrong columns.
//
// Repro:
//   customers schema: id(0) | name(1) | email(2) | country(3) | created_at(4)
//   DROP COLUMN name
//   New schema:       id(0) | email(1) | country(2) | created_at(3)
//   Heap rows:        id(0) | name(1)  | email(2)   | country(3) | created_at(4)
//   SELECT country FROM customers → schema says country is Values[2],
//   but Values[2] is actually the email column — corrupt read.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG004_DropColumnHeapMisalignment(t *testing.T) {
	db := newTestDB(t)

	// Capture a known country value for customer id=1 BEFORE the drop.
	// customers schema: id(0), name(1), email(2), country(3), created_at(4)
	before := db.run(t, "SELECT country FROM customers WHERE id = 1")
	require.Len(t, before.Rows, 1, "pre-drop: expected one row for id=1")
	require.Len(t, before.Rows[0], 1, "pre-drop: expected one column")
	countryBefore := before.Rows[0][0].StrVal

	// Also capture the email for id=1 to detect the misalignment symptom.
	beforeEmail := db.run(t, "SELECT email FROM customers WHERE id = 1")
	require.Len(t, beforeEmail.Rows, 1)
	emailBefore := beforeEmail.Rows[0][0].StrVal

	// Drop the 'name' column (index 1, in the middle of the schema).
	// After this the catalog schema is:
	//   id(0) | email(1) | country(2) | created_at(3)
	// but heap rows are still:
	//   id(0) | name(1) | email(2) | country(3) | created_at(4)
	db.run(t, "ALTER TABLE customers DROP COLUMN name")

	// Now SELECT country — should still return the country value.
	after := db.run(t, "SELECT country FROM customers WHERE id = 1")
	require.Len(t, after.Rows, 1, "post-drop: expected one row for id=1")
	require.Len(t, after.Rows[0], 1, "post-drop: expected one column")
	countryAfter := after.Rows[0][0].StrVal

	// BUG: because heap rows are not trimmed, schema index 2 (country) now reads
	// heap position 2 which is the old 'email' value.  The assertion below will
	// fail when the bug is present.
	assert.Equal(t, countryBefore, countryAfter,
		"BUG-004: SELECT country returned %q after DROP COLUMN name, but expected %q — "+
			"heap row still has the old 'email' value (%q) at position 2 which the engine "+
			"incorrectly maps to 'country'",
		countryAfter, countryBefore, emailBefore)
}
