package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/storage"
)

// runRaw011 runs the full pipeline for sql and returns (result, error) without
// calling t.Fatal on any intermediate step — so we can observe where the failure
// occurs.
func runRaw011(t *testing.T, db *testDB, sql string) (*Result, error) {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	a := analyzer.New(db.cat)
	if err := a.Analyze(stmt); err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	if err != nil {
		return nil, fmt.Errorf("logical build: %w", err)
	}

	opt := optimizer.New()
	lplan = opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	if err != nil {
		return nil, fmt.Errorf("physical build: %w", err)
	}

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	result, err := Execute(pplan, ctx)
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	return result, nil
}

// TestProbe_BUG011_CreateTableAsSelect probes BUG-011:
//
// CREATE TABLE ... AS SELECT is not implemented. The CreateTableStatement AST
// node (ast/nodes.go) has no SelectSource field, parseCreateTable() does not
// parse the AS SELECT syntax, and no logical/physical plan node handles it.
// The parser emits an error at the AS keyword because it unconditionally
// expects a LPAREN (column list) immediately after the table name.
//
// Repro SQL:
//
//	CREATE TABLE summary AS SELECT dept, COUNT(*) cnt FROM employees GROUP BY dept;
//
// Expected: Table created and populated from the query result.
// Actual:   Parse error at the AS keyword.
func TestProbe_BUG011_CreateTableAsSelect(t *testing.T) {
	// Build a fresh catalog + storage with an employees table.
	cat := catalog.New()
	store := storage.New()

	empTbl := &catalog.Table{
		Name: "employees",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, Index: 0},
			{Name: "dept", Type: catalog.TypeText, Index: 1},
		},
	}
	if err := cat.Register(empTbl); err != nil {
		t.Fatalf("setup: register employees: %v", err)
	}
	if err := store.CreateTable("employees"); err != nil {
		t.Fatalf("setup: create employees storage: %v", err)
	}

	db := &testDB{cat: cat, store: store}

	// Seed rows so the GROUP BY query yields results.
	db.run(t, "INSERT INTO employees (id, dept) VALUES (1, 'eng')")
	db.run(t, "INSERT INTO employees (id, dept) VALUES (2, 'eng')")
	db.run(t, "INSERT INTO employees (id, dept) VALUES (3, 'hr')")

	reproSQL := "CREATE TABLE summary AS SELECT dept, COUNT(*) cnt FROM employees GROUP BY dept"

	result, pipelineErr := runRaw011(t, db, reproSQL)

	if pipelineErr != nil {
		// Bug confirmed: the pipeline rejected CREATE TABLE ... AS SELECT.
		errMsg := pipelineErr.Error()
		t.Logf("BUG-011 CONFIRMED: pipeline error: %s", errMsg)
		t.Fatalf(
			"BUG-011: CREATE TABLE ... AS SELECT is not implemented. "+
				"Pipeline failed with: %s", errMsg)
	}

	// If the pipeline did not error, verify the table was actually created and
	// contains the expected aggregated rows.
	t.Logf("CREATE TABLE ... AS SELECT did not error (result rows=%d)", func() int {
		if result != nil {
			return len(result.Rows)
		}
		return -1
	}())

	summaryResult, summaryErr := runRaw011(t, db, "SELECT dept, cnt FROM summary ORDER BY dept")
	if summaryErr != nil {
		t.Fatalf(
			"BUG-011: CREATE TABLE ... AS SELECT appeared to succeed but 'summary' "+
				"cannot be queried: %v", summaryErr)
	}

	if len(summaryResult.Rows) != 2 {
		t.Fatalf(
			"BUG-011: expected 2 rows in 'summary' (eng, hr), got %d: %s",
			len(summaryResult.Rows), bug011FormatRows(summaryResult))
	}

	t.Logf("CREATE TABLE ... AS SELECT succeeded; summary rows:\n%s", bug011FormatRows(summaryResult))
}

func bug011FormatRows(r *Result) string {
	if r == nil {
		return "<nil result>"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("columns=%v\n", r.Columns))
	for i, row := range r.Rows {
		vals := make([]string, len(row))
		for j, v := range row {
			vals[j] = v.String()
		}
		sb.WriteString(fmt.Sprintf("  row[%d]: %s\n", i, strings.Join(vals, ", ")))
	}
	return sb.String()
}
