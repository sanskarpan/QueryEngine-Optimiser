package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/storage"
)

// TestProbe_BUG006_AlterTableRenameColumn verifies BUG-006:
//
// parseAlterTable() only handles "RENAME TO <new_table_name>". It has no
// branch for "RENAME COLUMN <old> TO <new>". When the parser sees
//
//	ALTER TABLE employees RENAME COLUMN salary TO compensation
//
// it enters the RENAME case, then checks whether the next token is the
// identifier "TO". Because the next token is the keyword COLUMN (not an
// IDENT "TO"), the TO-check is skipped. The parser then reads COLUMN (a
// keyword) as the new table name, producing Action="RENAME", NewName="COLUMN"
// — a silent mis-parse that renames the table to the word "COLUMN" instead of
// renaming the salary column to compensation.
func TestProbe_BUG006_AlterTableRenameColumn(t *testing.T) {
	const reproSQL = "ALTER TABLE employees RENAME COLUMN salary TO compensation"

	// ── Step 1: Inspect the parser output directly ────────────────────────────
	p := parser.New(reproSQL)
	stmt, parseErr := p.ParseStatement()

	if parseErr != nil {
		t.Fatalf(
			"BUG-006 CONFIRMED (parse error): ALTER TABLE … RENAME COLUMN … TO … is not supported. "+
				"Parser returned error: %v. Expected: column renamed successfully.",
			parseErr,
		)
	}

	// Log what the parser produced so we can see the mis-parse.
	t.Logf("Parsed AST: %#v", stmt)

	// ── Step 2: Build a fresh DB with an employees table ─────────────────────
	cat := catalog.New()
	store := storage.New()
	empTbl := &catalog.Table{
		Name: "employees",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "salary", Type: catalog.TypeInt, Index: 1},
		},
	}
	if err := cat.Register(empTbl); err != nil {
		t.Fatalf("setup: register table: %v", err)
	}
	if err := store.CreateTable("employees"); err != nil {
		t.Fatalf("setup: create storage table: %v", err)
	}
	db := &testDB{cat: cat, store: store}
	db.run(t, "INSERT INTO employees (id, salary) VALUES (1, 50000)")

	// ── Step 3: Attempt the RENAME COLUMN via the full pipeline ──────────────
	// db.run() calls require.NoError at parse / analyze / build / execute steps.
	// Any failure surfaces BUG-006 directly. We use a sub-test to capture the
	// outcome rather than aborting the outer test immediately.
	var actualOutput string
	passed := t.Run("rename_column_subtest", func(sub *testing.T) {
		db.run(sub, reproSQL) // expect this to succeed for a correct implementation

		// After rename, querying "compensation" should work.
		sel := db.run(sub, "SELECT compensation FROM employees")
		colNames := strings.Join(sel.Columns, ", ")
		actualOutput = fmt.Sprintf(
			"SELECT compensation columns=%s rows=%d", colNames, len(sel.Rows),
		)
		sub.Logf("Post-rename output: %s", actualOutput)
	})

	if !passed {
		actualOutput = "pipeline rejected ALTER TABLE employees RENAME COLUMN salary TO compensation " +
			"(likely parsed as table rename to 'COLUMN', or parse/execute error)"
		t.Fatalf(
			"BUG-006 CONFIRMED: ALTER TABLE … RENAME COLUMN … TO … is unsupported. "+
				"The parser's parseAlterTable() RENAME branch only handles RENAME TO <table_name>. "+
				"It has no 'RENAME COLUMN old TO new' path. "+
				"Actual output: %s",
			actualOutput,
		)
	}

	// Sanity-check: the renamed column must appear in the result.
	if !strings.Contains(actualOutput, "compensation") {
		t.Fatalf(
			"BUG-006: rename appeared to succeed but column 'compensation' not found in result. actual=%s",
			actualOutput,
		)
	}
}
