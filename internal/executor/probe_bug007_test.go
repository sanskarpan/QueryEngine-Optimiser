package executor

// TestProbe_BUG007_UpdateWhereEvalExprErrSilentlyDiscarded verifies BUG-007:
//
// When EvalExpr returns an error inside the UpdateWhere updater closure
// (update.go lines 88-91), the error is silently discarded via "continue".
// The row value is left unchanged, BUT UpdateWhere still increments count
// because the predicate matched. This means:
//   - rows_affected is overcounted (returns 1 instead of 0 / an error)
//   - partial updates are silently accepted as full updates
//   - no error is ever surfaced to the caller
//
// Repro:
//
//	INSERT INTO products (id, name, category, price) VALUES (7001, 'Widget', 'gadgets', 9.99)
//	UPDATE products SET price = (SELECT bad_col FROM nonexistent) WHERE id = 7001
//
// Expected: an error returned to the caller (or rows_affected = 0)
// Actual (bug): rows_affected = 1, no error, price unchanged

import (
	"fmt"
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/stretchr/testify/require"
)

// executeRawNoAnalyze parses and runs sql against db, skipping analysis so
// that the error (if any) surfaces at execution time rather than being caught
// by the analyzer. Returns (result, error) — neither is require'd.
func executeRawNoAnalyze(t *testing.T, db *testDB, sql string) (*Result, error) {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse: %s", sql)

	// Intentionally skip analyzer.Analyze so we can observe the execution-time
	// behaviour described in the bug report.

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
	return Execute(pplan, ctx)
}

func TestProbe_BUG007_UpdateWhereEvalExprErrSilentlyDiscarded(t *testing.T) {
	db := newTestDB(t)

	// Insert a known product row that we will try to UPDATE.
	db.run(t, `INSERT INTO products (id, name, category, price, stock_quantity) VALUES (7001, 'Widget', 'gadgets', 9.99, 5)`)

	// Confirm the row exists with the expected price before the update attempt.
	beforeResult := db.run(t, `SELECT price FROM products WHERE id = 7001`)
	require.Len(t, beforeResult.Rows, 1, "setup: expected product 7001 to exist")
	priceBefore := beforeResult.Rows[0][0]
	t.Logf("price before UPDATE attempt: %v (type=%v)", priceBefore, priceBefore.Type)

	// Attempt the UPDATE with a bad subquery — the assignment expression refers
	// to a nonexistent table, so EvalExpr will return an error inside the
	// UpdateWhere updater closure.
	badUpdate := `UPDATE products SET price = (SELECT bad_col FROM nonexistent) WHERE id = 7001`
	result, execErr := executeRawNoAnalyze(t, db, badUpdate)

	t.Logf("UPDATE error returned: %v", execErr)
	if result != nil && len(result.Rows) > 0 {
		t.Logf("UPDATE rows_affected: %v", result.Rows[0][0])
	}

	// Check the actual price after the UPDATE attempt.
	afterResult := db.run(t, `SELECT price FROM products WHERE id = 7001`)
	require.Len(t, afterResult.Rows, 1, "expected product 7001 to still exist after UPDATE")
	priceAfter := afterResult.Rows[0][0]
	t.Logf("price after UPDATE attempt: %v (type=%v)", priceAfter, priceAfter.Type)

	// Characterise the observed output for reporting.
	var actualParts []string
	if execErr != nil {
		actualParts = append(actualParts, fmt.Sprintf("execErr=%v", execErr))
	} else {
		actualParts = append(actualParts, "execErr=<nil>")
	}
	if result != nil && len(result.Rows) > 0 {
		actualParts = append(actualParts, fmt.Sprintf("rows_affected=%v", result.Rows[0][0].IntVal))
	}
	actualParts = append(actualParts, fmt.Sprintf("price_before=%v price_after=%v", priceBefore, priceAfter))
	actualOutput := strings.Join(actualParts, "; ")
	t.Logf("Actual output: %s", actualOutput)

	// --- Assert correct behaviour ---

	// 1. The caller MUST receive an error when the assignment expression fails.
	if execErr == nil {
		rowsAffected := int64(0)
		if result != nil && len(result.Rows) > 0 {
			rowsAffected = result.Rows[0][0].IntVal
		}
		// Bug is confirmed: no error returned and rows_affected was overcounted.
		if rowsAffected > 0 {
			t.Errorf(
				"BUG-007 CONFIRMED: UPDATE with bad subquery returned rows_affected=%d (expected error or 0). "+
					"Error was silently discarded inside the UpdateWhere updater closure (update.go:88-91). "+
					"Actual output: %s",
				rowsAffected, actualOutput,
			)
		} else {
			// No error but also no count — still wrong: should have errored.
			t.Errorf(
				"BUG-007 CONFIRMED (no-error variant): UPDATE with bad subquery returned nil error "+
					"and rows_affected=0 instead of surfacing the EvalExpr error. "+
					"Actual output: %s",
				actualOutput,
			)
		}
	}

	// 2. The price must NOT have changed (assignment was skipped silently).
	if execErr == nil {
		if priceAfter.Type == catalog.TypeFloat && priceBefore.Type == catalog.TypeFloat {
			if priceAfter.FloatVal != priceBefore.FloatVal {
				t.Errorf(
					"BUG-007: price was mutated despite EvalExpr error: before=%v after=%v",
					priceBefore.FloatVal, priceAfter.FloatVal,
				)
			} else {
				t.Logf("Price is correctly unchanged (%v), but the lack of error is still the bug.", priceAfter.FloatVal)
			}
		}
	}
}
