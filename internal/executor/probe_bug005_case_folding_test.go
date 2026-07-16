package executor

import (
	"strings"
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/optimizer/rule"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProbe_BUG005_CaseExprConstantFolding probes BUG-005:
// foldExpr in constant_folding.go handles BinaryExpr and UnaryExpr but has no
// case for CaseExpr. Constant sub-expressions inside CASE WHEN ... THEN ... END
// are never folded. Specifically, CASE WHEN 1=1 THEN 'yes' ELSE 'no' END should
// be folded to the string literal 'yes' at plan time, but instead remains as a
// CaseExpr node in the optimized logical plan.
//
// Additionally, collectAgg in builder.go does not recurse into CaseExpr, so
// aggregate functions embedded inside CASE WHEN expressions are never registered.
//
// Expected after fix: the optimized plan's projection contains a StringLiteral
// 'yes' rather than a CaseExpr node, and the executed result is 'yes'.
//
// Repro SQL: SELECT CASE WHEN 1=1 THEN 'yes' ELSE 'no' END FROM customers LIMIT 1
func TestProbe_BUG005_CaseExprConstantFolding(t *testing.T) {
	db := newTestDB(t)

	// -----------------------------------------------------------------------
	// Part 1: End-to-end execution
	// The runtime evaluator (evalCase) does handle CaseExpr, so the query
	// should return the correct value 'yes'. This verifies baseline correctness.
	// -----------------------------------------------------------------------
	result := db.run(t, "SELECT CASE WHEN 1=1 THEN 'yes' ELSE 'no' END FROM customers LIMIT 1")
	require.Len(t, result.Rows, 1, "expected exactly one row")
	require.Len(t, result.Rows[0], 1, "expected exactly one column")

	actualVal := result.Rows[0][0]
	t.Logf("Runtime result: type=%v strVal=%q isNull=%v", actualVal.Type, actualVal.StrVal, actualVal.IsNull)

	// Runtime should return 'yes' because evalCase works correctly at runtime.
	assert.Equal(t, "yes", actualVal.StrVal,
		"runtime should evaluate CASE WHEN 1=1 THEN 'yes' ELSE 'no' END to 'yes'")

	// -----------------------------------------------------------------------
	// Part 2: Optimizer-level check — constant folding should have removed the
	// CaseExpr and replaced it with a StringLiteral('yes').
	//
	// With the bug present: foldExpr has no case for *ast.CaseExpr, so the
	// expression is returned unchanged. PrintExpr of a CaseExpr returns "CASE..."
	// and the plan string will contain "CASE..." instead of the folded literal.
	// -----------------------------------------------------------------------
	sql := "SELECT CASE WHEN 1=1 THEN 'yes' ELSE 'no' END FROM customers LIMIT 1"

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err, "parse")

	cat := db.cat
	a := analyzer.New(cat)
	require.NoError(t, a.Analyze(stmt), "analyze")

	b := logical.NewBuilder(cat)
	lplan, err := b.BuildStatement(stmt)
	require.NoError(t, err, "build logical plan")

	// Apply only the ConstantFolding rule so we can isolate its effect.
	cf := &rule.ConstantFolding{}
	optimized, changed := cf.Apply(lplan)

	t.Logf("ConstantFolding changed: %v", changed)
	planStr := logical.PrintPlan(optimized)
	t.Logf("Optimized plan:\n%s", planStr)

	// Find the projection node and inspect its expressions.
	var projectNode *logical.LogicalProject
	walkPlan(optimized, func(n logical.Plan) {
		if proj, ok := n.(*logical.LogicalProject); ok && projectNode == nil {
			projectNode = proj
		}
	})
	require.NotNil(t, projectNode, "expected a LogicalProject node in the plan")

	// With the bug present, the projection still holds a *ast.CaseExpr.
	// After the fix, it should hold a *ast.StringLiteral with Value "yes".
	hasCaseExpr := false
	hasStringLiteral := false
	var foldedValue string

	for _, expr := range projectNode.Expressions {
		inner := unwrapAlias(expr)
		switch v := inner.(type) {
		case *ast.CaseExpr:
			hasCaseExpr = true
			t.Logf("BUG-005 CONFIRMED: projection still contains *ast.CaseExpr after ConstantFolding")
		case *ast.StringLiteral:
			hasStringLiteral = true
			foldedValue = v.Value
			t.Logf("Projection contains StringLiteral %q (folded correctly)", v.Value)
		default:
			t.Logf("Projection expression type: %T", inner)
		}
	}

	if hasCaseExpr {
		// Bug is present: the CASE expression was not folded.
		// Log the plan string showing "CASE..." instead of the literal.
		t.Logf("Plan string contains CASE token: %v", strings.Contains(planStr, "CASE"))

		// Assert the bug: constant folding should have replaced the CaseExpr.
		assert.False(t, hasCaseExpr,
			"BUG-005: foldExpr does not handle *ast.CaseExpr; "+
				"CASE WHEN 1=1 THEN 'yes' ELSE 'no' END was not folded to the literal 'yes'. "+
				"The optimized plan still contains a CaseExpr node.")
	} else if hasStringLiteral {
		assert.Equal(t, "yes", foldedValue,
			"constant-folded CASE WHEN 1=1 THEN 'yes' ELSE 'no' END should produce 'yes'")
		t.Logf("BUG-005 NOT present (or fixed): CaseExpr was correctly folded to %q", foldedValue)
	} else {
		t.Logf("Unexpected expression type in projection; plan:\n%s", planStr)
	}
}

// TestProbe_BUG005_CollectAggInsideCaseExpr probes the second part of BUG-005:
// collectAgg in builder.go does not recurse into CaseExpr, so aggregate functions
// embedded inside CASE WHEN expressions (e.g. CASE WHEN x > 0 THEN SUM(x) END)
// are never registered in the aggregate list, causing them to be left as
// unevaluated FunctionCall nodes in the projection above the aggregate operator.
//
// Repro SQL: SELECT CASE WHEN 1 > 0 THEN SUM(amount) ELSE 0 END FROM orders
func TestProbe_BUG005_CollectAggInsideCaseExpr(t *testing.T) {
	db := newTestDB(t)

	// This query has SUM(amount) embedded inside a CASE WHEN expression.
	// If collectAgg does not recurse into CaseExpr, SUM(amount) is never
	// registered and the plan will either error or produce a wrong result.
	sql := "SELECT CASE WHEN 1 > 0 THEN SUM(amount) ELSE 0 END FROM orders"

	result, execErr := runAllowError(t, db, sql)

	if execErr != nil {
		t.Logf("BUG-005 (collectAgg): execution returned error: %v", execErr)
		// An error is expected if the aggregate inside the CASE is not collected.
		// We log it but do NOT assert.NoError here — this IS the bug manifestation.
		t.Logf("BUG-005 CONFIRMED (collectAgg): SUM(amount) inside CASE WHEN was not collected as aggregate, execution failed")
		// Assert that there should be no error (this will fail, confirming the bug):
		assert.NoError(t, execErr,
			"BUG-005: collectAgg does not recurse into CaseExpr; "+
				"SUM(amount) inside CASE WHEN was never registered as an aggregate, "+
				"causing a runtime error instead of returning the sum value")
		return
	}

	// If we got a result, check it looks reasonable.
	require.NotNil(t, result)
	require.Len(t, result.Rows, 1, "GROUP-less aggregate should return exactly one row")
	require.Len(t, result.Rows[0], 1, "expected exactly one column")

	val := result.Rows[0][0]
	t.Logf("Result value: type=%v intVal=%v floatVal=%v strVal=%q isNull=%v",
		val.Type, val.IntVal, val.FloatVal, val.StrVal, val.IsNull)

	// If 1 > 0 is true (which it always is), the result should be the sum of amounts.
	// An incorrect result of 0 (the ELSE branch) or NULL would indicate the aggregate
	// was never evaluated inside the CASE.
	assert.False(t, val.IsNull,
		"BUG-005: result should not be NULL; if aggregate was missed, the CASE falls through to ELSE or NULL")
	t.Logf("BUG-005 NOT present (or fixed): got result %v", val)
}

// walkPlan visits every node in a logical plan tree.
func walkPlan(plan logical.Plan, fn func(logical.Plan)) {
	if plan == nil {
		return
	}
	fn(plan)
	for _, child := range plan.Children() {
		walkPlan(child, fn)
	}
}

// unwrapAlias strips a top-level AliasExpr wrapper.
func unwrapAlias(expr ast.Expression) ast.Expression {
	if a, ok := expr.(*ast.AliasExpr); ok {
		return a.Expr
	}
	return expr
}
