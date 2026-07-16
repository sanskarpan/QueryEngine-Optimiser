package executor

// TestProbe_BUG003_AlterTableRenameStorageSync verifies BUG-003:
// ALTER TABLE RENAME TO updates the catalog entry but does NOT rename the
// corresponding HeapTable entry in the storage layer. After RENAME, the
// catalog has the new name, but storage still maps the old key, so any
// subsequent query against the renamed table fails with "table not found
// in storage".
//
// Repro SQL:
//
//	ALTER TABLE customers RENAME TO clients;
//	SELECT * FROM clients LIMIT 1;
//
// Expected: SELECT returns a row from clients.
// Actual (buggy): executor panics or returns an error because storage still
// holds the table under the key "customers", not "clients".

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG003_AlterTableRenameStorageSync(t *testing.T) {
	db := newTestDB(t)

	// Step 1: rename an existing seed table.
	db.run(t, `ALTER TABLE customers RENAME TO clients`)

	// Step 2: query the renamed table — should succeed and return rows.
	r := db.run(t, `SELECT id FROM clients LIMIT 1`)

	require.Len(t, r.Rows, 1, "expected 1 row from renamed table 'clients'")
	assert.False(t, r.Rows[0][0].IsNull, "id column should not be NULL")
}
