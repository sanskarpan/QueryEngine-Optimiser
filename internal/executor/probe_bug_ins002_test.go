package executor

// TestProbe_BUG002_InsertSelectUnsupported probes BUG-002:
// INSERT INTO t SELECT ... is completely unsupported.
// The AST InsertStatement has no SelectSource field, the parser's parseInsert()
// unconditionally expects a VALUES keyword after the column list, the logical
// planner's buildInsert() only handles ValueRows, and InsertOp only iterates
// ValueRows.  Any attempt to use INSERT ... SELECT is therefore a parse error.

import (
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestProbe_BUG002_InsertSelectUnsupported(t *testing.T) {
	// Build a fresh in-memory database with two tables:
	//   employees(id INT, name TEXT, active BOOL)
	//   archive(id INT, name TEXT)
	cat := catalog.New()
	store := storage.New()

	employees := &catalog.Table{
		Name: "employees",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "name", Type: catalog.TypeText, Index: 1},
			{Name: "active", Type: catalog.TypeBool, Index: 2},
		},
	}
	require.NoError(t, cat.Register(employees))
	require.NoError(t, store.CreateTable("employees"))

	archive := &catalog.Table{
		Name: "archive",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "name", Type: catalog.TypeText, Index: 1},
		},
	}
	require.NoError(t, cat.Register(archive))
	require.NoError(t, store.CreateTable("archive"))

	// Populate employees with two active rows and one inactive row.
	emp := store.MustGetTable("employees")
	emp.Insert(storage.Tuple{Values: []catalog.Value{
		catalog.IntValue(1), catalog.TextValue("Alice"), catalog.BoolValue(true),
	}})
	emp.Insert(storage.Tuple{Values: []catalog.Value{
		catalog.IntValue(2), catalog.TextValue("Bob"), catalog.BoolValue(false),
	}})
	emp.Insert(storage.Tuple{Values: []catalog.Value{
		catalog.IntValue(3), catalog.TextValue("Carol"), catalog.BoolValue(false),
	}})

	db := &testDB{cat: cat, store: store}

	// This INSERT ... SELECT should copy the two inactive employees into archive.
	// BUG-002 predicts this will fail at parse time because the parser requires
	// VALUES after the column list and has no branch for SELECT.
	result := db.run(t, `INSERT INTO archive (id, name) SELECT id, name FROM employees WHERE active = false`)

	// If we somehow get here without a parse error, verify correctness.
	require.NotNil(t, result)

	// The archive should now contain the two inactive employees.
	archiveResult := db.run(t, `SELECT id, name FROM archive ORDER BY id`)
	require.Equal(t, 2, len(archiveResult.Rows),
		"expected 2 rows in archive after INSERT ... SELECT, got %d", len(archiveResult.Rows))
}
