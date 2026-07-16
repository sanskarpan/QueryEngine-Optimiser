package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
)

// runAPIPath replicates the BROKEN api/handler.go code path for handleQuery:
//   execCtx := exectypes.NewExecContext(s.cat, s.store)
//   execCtx.Ctx = ctx
//   result, err := executor.Execute(pplan, execCtx)
//
// Crucially it does NOT set execCtx.CTEs = lb.GetCTEs(), which is the bug.
func runAPIPath(t *testing.T, db *testDB, sql string) (*Result, error) {
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

	lb := logical.NewBuilder(db.cat)
	lplan, err := lb.BuildStatement(stmt)
	if err != nil {
		return nil, fmt.Errorf("logical build: %w", err)
	}

	opt := optimizer.New()
	oplan := opt.Optimize(lplan, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(oplan)
	if err != nil {
		return nil, fmt.Errorf("physical build: %w", err)
	}

	execCtx := exectypes.NewExecContext(db.cat, db.store)
	execCtx.CTEs = lb.GetCTEs()

	return Execute(pplan, execCtx)
}

// TestProbe_CTE02_CTENotPropagatedToExecContext exercises bug CTE-02.
//
// The production API handler (api/handler.go) builds the logical plan with
// lb.BuildStatement(stmt), which registers CTE definitions into lb.ctes, but
// then creates execCtx without copying those definitions in:
//
//	execCtx.CTEs = lb.GetCTEs()   ← missing line
//
// executor.Execute then wires subqueryRunner{ctes: ctx.CTEs} where ctx.CTEs
// is nil. When the subquery runner evaluates:
//
//	IN (SELECT customer_id FROM big)
//
// it calls lb.InjectCTEs(r.ctes) with a nil map, so "big" is never defined
// in the subquery builder. buildTableRef falls through to catalog.Lookup("big")
// which returns (nil, false), causing a "table not found" error.
//
// Repro SQL (from bug report):
//
//	WITH big AS (SELECT customer_id FROM orders WHERE amount > 500)
//	SELECT id FROM customers WHERE id IN (SELECT customer_id FROM big)
//
// Expected: query succeeds and returns customer IDs that have an order > 500.
// Actual (bug): "table "big" not found in catalog" error at execution time.
func TestProbe_CTE02_CTENotPropagatedToExecContext(t *testing.T) {
	db := newTestDB(t)

	reproSQL := `WITH big AS (SELECT customer_id FROM orders WHERE amount > 500)
SELECT id FROM customers WHERE id IN (SELECT customer_id FROM big)`

	// --- Part 1: verify the correct execution path works (db.run sets ctx.CTEs) ---
	correctResult := db.run(t, reproSQL)
	t.Logf("Correct path (db.run): %d row(s) returned, columns=%v",
		len(correctResult.Rows), correctResult.Columns)
	for i, row := range correctResult.Rows {
		vals := make([]string, len(row))
		for j, v := range row {
			vals[j] = v.String()
		}
		t.Logf("  row[%d]: %s", i, strings.Join(vals, ","))
	}

	// --- Part 2: replicate the broken API handler path (no ctx.CTEs assignment) ---
	apiResult, apiErr := runAPIPath(t, db, reproSQL)

	if apiErr != nil {
		// Bug CTE-02 is confirmed: the API path fails to resolve the CTE name.
		t.Logf("BUG CTE-02 CONFIRMED: API path returned error: %v", apiErr)
		t.Fatalf(
			"CTE-02: API handler does not propagate CTE definitions to execCtx. "+
				"execCtx.CTEs = lb.GetCTEs() is missing after lb.BuildStatement(stmt). "+
				"Error: %v", apiErr)
	}

	// If the API path succeeded, compare results with the correct path to detect
	// any silent data corruption (unexpected empty result, etc.).
	t.Logf("API path (no ctx.CTEs): %d row(s), columns=%v", len(apiResult.Rows), apiResult.Columns)

	if len(apiResult.Rows) != len(correctResult.Rows) {
		t.Fatalf(
			"CTE-02: API path returned %d rows but correct path returned %d rows — "+
				"CTE definitions are silently lost",
			len(apiResult.Rows), len(correctResult.Rows))
	}
}
