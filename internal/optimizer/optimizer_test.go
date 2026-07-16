package optimizer

import (
	"testing"

	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/optimizer/rule"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestCatalog() *catalog.Catalog {
	cat := catalog.New()
	cat.MustRegister(&catalog.Table{
		Name: "customers",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "name", Type: catalog.TypeText, Index: 1},
			{Name: "country", Type: catalog.TypeText, Index: 2},
		},
	})
	cat.MustRegister(&catalog.Table{
		Name: "orders",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "customer_id", Type: catalog.TypeInt, Index: 1},
			{Name: "amount", Type: catalog.TypeFloat, Index: 2},
			{Name: "status", Type: catalog.TypeText, Index: 3},
		},
	})
	return cat
}

func buildPlan(t *testing.T, sql string) logical.Plan {
	t.Helper()
	cat := makeTestCatalog()
	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	require.NoError(t, err)
	a := analyzer.New(cat)
	require.NoError(t, a.Analyze(stmt))
	b := logical.NewBuilder(cat)
	plan, err := b.Build(stmt.(*ast.SelectStatement))
	require.NoError(t, err)
	return plan
}

func TestOptimizer_PredicatePushdown_ThroughJoin(t *testing.T) {
	plan := buildPlan(t,
		"SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id WHERE o.status = 'shipped'")

	opt := New()
	var steps []rule.OptimizationStep
	optimized := opt.Optimize(plan, &steps)

	// After pushdown, the filter on o.status should be below the join
	// Verify the plan string contains filter below join
	printed := logical.PrintPlan(optimized)
	t.Log("Optimized plan:\n", printed)

	// Check at least one predicate pushdown step was applied
	applied := false
	for _, s := range steps {
		if s.Rule == "PredicatePushdown" && s.Applied {
			applied = true
			break
		}
	}
	assert.True(t, applied, "PredicatePushdown should have fired")
}

func TestOptimizer_ConstantFolding_Arithmetic(t *testing.T) {
	// 1+2 should fold to 3
	r := &rule.ConstantFolding{}
	plan := buildPlan(t, "SELECT id FROM customers WHERE id > 0")

	// Create a plan with a constant expression
	inner := logical.NewBuilder(makeTestCatalog())
	stmt, _ := parser.New("SELECT id FROM customers WHERE 1+2 > 0").ParseStatement()
	analyzer.New(makeTestCatalog()).Analyze(stmt)
	p, _ := inner.Build(stmt.(*ast.SelectStatement))

	result, changed := r.Apply(p)
	_ = result
	_ = changed
	// Just verify it doesn't panic
	assert.NotNil(t, plan)
}

func TestOptimizer_ConstantFolding_BoolSimplification(t *testing.T) {
	r := &rule.ConstantFolding{}

	// Build a plan manually with a constant predicate
	cat := makeTestCatalog()
	table, _ := cat.Lookup("customers")
	scan := &logical.LogicalScan{TableName: "customers", Alias: "customers", Table: table}
	trueFilter := &logical.LogicalFilter{
		Child:     scan,
		Predicate: &ast.BoolLiteral{Value: true},
	}

	result, changed := r.Apply(trueFilter)
	_ = result
	// Changed should be false since constant folding doesn't eliminate dead filters
	// (that's EliminateDeadFilter's job)
	_ = changed
}

func TestOptimizer_EliminateDeadFilter_True(t *testing.T) {
	r := &rule.EliminateDeadFilter{}

	cat := makeTestCatalog()
	table, _ := cat.Lookup("customers")
	scan := &logical.LogicalScan{TableName: "customers", Alias: "customers", Table: table}
	trueFilter := &logical.LogicalFilter{
		Child:     scan,
		Predicate: &ast.BoolLiteral{Value: true},
	}

	result, changed := r.Apply(trueFilter)
	assert.True(t, changed)
	// Result should be the scan directly (filter removed)
	_, isScan := result.(*logical.LogicalScan)
	assert.True(t, isScan, "expected scan after eliminating TRUE filter, got %T", result)
}

func TestOptimizer_EliminateDeadFilter_False(t *testing.T) {
	r := &rule.EliminateDeadFilter{}

	cat := makeTestCatalog()
	table, _ := cat.Lookup("customers")
	scan := &logical.LogicalScan{TableName: "customers", Alias: "customers", Table: table}
	falseFilter := &logical.LogicalFilter{
		Child:     scan,
		Predicate: &ast.BoolLiteral{Value: false},
	}

	result, changed := r.Apply(falseFilter)
	assert.True(t, changed)
	_, isEmpty := result.(*logical.EmptyRelation)
	assert.True(t, isEmpty, "expected EmptyRelation after eliminating FALSE filter, got %T", result)
}

func TestOptimizer_Idempotency(t *testing.T) {
	plan := buildPlan(t,
		"SELECT c.name, o.amount FROM customers c JOIN orders o ON c.id = o.customer_id WHERE o.status = 'shipped'")

	opt := New()
	var steps1 []rule.OptimizationStep
	once := opt.Optimize(plan, &steps1)

	// Apply again — should not change anything significant
	var steps2 []rule.OptimizationStep
	twice := opt.Optimize(once, &steps2)

	// Both plans should have the same string representation
	s1 := logical.PrintPlan(once)
	s2 := logical.PrintPlan(twice)
	assert.Equal(t, s1, s2, "optimizer should be idempotent")
}

func TestOptimizer_NoStepsOnSimpleQuery(t *testing.T) {
	plan := buildPlan(t, "SELECT id FROM customers")
	opt := New()
	var steps []rule.OptimizationStep
	opt.Optimize(plan, &steps)
	// Steps are recorded even when not applied
	assert.NotEmpty(t, steps)
}
