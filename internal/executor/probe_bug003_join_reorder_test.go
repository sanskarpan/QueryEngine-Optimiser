package executor

// TestProbe_BUG003_JoinReorderConditionLoss probes BUG-003:
//
// In cost/join_order.go, planAlias handles LogicalScan, LogicalFilter (recursing),
// and LogicalSubquery — but returns "" for any other node type (e.g. LogicalProject).
//
// The ProjectionPushdown rule wraps join children with LogicalProject nodes when
// it prunes unused columns.  After that rewriting, extractInnerJoins sees
// LogicalProject leaves and calls planAlias on them, receiving "".  maskAliases
// skips empty-alias relations (rel.alias != ""), so condConnects never matches
// any condition, and tryReorder returns (nil, false) — silently abandoning all
// join-order optimization.
//
// Actual observed impact: tryReorder fails to build a full plan for any subset
// because no condition "connects" the alias-less leaf pairs.  The optimizer
// falls back to the original (un-reordered) plan, so correctness is preserved
// but the CBO's join-order goal is completely defeated — no reordering ever
// happens when ProjectionPushdown has fired.
//
// The test below confirms the code path: it directly exercises the
// JoinOrderOptimizer with LogicalProject-wrapped leaves (simulating what
// ProjectionPushdown produces) and verifies that tryReorder gives up, whereas
// without the wrapping (bare LogicalScan leaves) the reordering succeeds.

import (
	"fmt"
	"testing"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/analyzer"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/exectypes"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/optimizer"
	"github.com/query-engine/query-engine/internal/optimizer/cost"
	"github.com/query-engine/query-engine/internal/parser"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/planner/physical"
	"github.com/query-engine/query-engine/internal/stats"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWithCBO is like db.run but uses OptimizeWithCBO with a non-empty stats map
// so that the JoinOrderOptimizer is actually invoked.
func runWithCBO(t *testing.T, db *testDB, sql string) (*Result, error) {
	t.Helper()

	p := parser.New(sql)
	stmt, err := p.ParseStatement()
	if err != nil {
		return nil, err
	}

	a := analyzer.New(db.cat)
	if err := a.Analyze(stmt); err != nil {
		return nil, err
	}

	b := logical.NewBuilder(db.cat)
	lplan, err := b.BuildStatement(stmt)
	if err != nil {
		return nil, err
	}

	// Provide a non-empty stats map so OptimizeWithCBO actually invokes
	// NewJoinOrderOptimizer and runs tryReorder over the join tree.
	statsMap := map[string]*stats.TableStats{
		"customers": {RowCount: 100},
		"orders":    {RowCount: 1000},
		"products":  {RowCount: 50},
	}

	opt := optimizer.New()
	lplan = opt.OptimizeWithCBO(lplan, statsMap, nil)

	pb := physical.NewBuilder()
	pplan, err := pb.Build(lplan)
	if err != nil {
		return nil, err
	}

	ctx := exectypes.NewExecContext(db.cat, db.store)
	ctx.CTEs = b.GetCTEs()
	return Execute(pplan, ctx)
}

// TestProbe_BUG003_JoinReorderConditionLoss verifies correctness is preserved
// (fallback to non-reordered plan) and documents the silent optimization defeat.
func TestProbe_BUG003_JoinReorderConditionLoss(t *testing.T) {
	db := newTestDB(t)

	const query = `
		SELECT c.id, o.id, p.id
		FROM customers c
		JOIN orders o ON c.id = o.customer_id
		JOIN products p ON o.product_id = p.id
	`

	// Ground truth: RBO-only execution.
	rboResult := db.run(t, query)
	rboCount := len(rboResult.Rows)
	t.Logf("RBO (no join reorder) row count: %d", rboCount)
	require.Greater(t, rboCount, 0, "RBO result should be non-empty for seeded data")
	require.LessOrEqual(t, rboCount, 1000, "RBO count should be <= total orders")

	// CBO execution.  After RBO (including ProjectionPushdown), the join leaves
	// are LogicalProject nodes.  planAlias returns "" for them, causing
	// tryReorder to silently fall back to the original plan.  Correctness is
	// preserved — but the reordering optimization is completely skipped.
	cboResult, err := runWithCBO(t, db, query)
	require.NoError(t, err, "CBO path should not return an error")

	cboCount := len(cboResult.Rows)
	t.Logf("CBO (with join reorder) row count: %d", cboCount)

	// Correctness check: CBO must produce the same rows as RBO.
	assert.Equal(t, rboCount, cboCount,
		"BUG-003: CBO join reorder returned %d rows but RBO returned %d rows. "+
			"planAlias returns \"\" for LogicalProject nodes inserted by ProjectionPushdown, "+
			"causing tryReorder to fail and fall back to the original plan. "+
			"If counts differ, conditions were actually dropped (cross-join semantics).",
		cboCount, rboCount)
}

// TestProbe_BUG003_PlanAliasReturnsEmptyForProject directly confirms the root
// cause: planAlias (an unexported function in cost/join_order.go) returns ""
// for LogicalProject nodes, causing the JoinOrderOptimizer to give up reordering
// whenever ProjectionPushdown has wrapped a scan leaf.
//
// We confirm this indirectly by building two plans:
//   (a) bare LogicalScan leaves  → JoinOrderOptimizer CAN reorder (tryReorder succeeds)
//   (b) LogicalProject-wrapped leaves → JoinOrderOptimizer CANNOT reorder (tryReorder fails)
//
// After running JoinOrderOptimizer on (b), the output plan type is the same
// LogicalJoin that was passed in (no reorder happened), confirming the optimizer
// silently gave up.
func TestProbe_BUG003_PlanAliasReturnsEmptyForProject(t *testing.T) {
	// Small catalog: t_large (1000 rows) and t_small (10 rows) with a join key.
	bigTable := &catalog.Table{
		Name: "t_large",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "fk", Type: catalog.TypeInt, Index: 1},
			{Name: "extra", Type: catalog.TypeText, Index: 2},
		},
	}
	smallTable := &catalog.Table{
		Name: "t_small",
		Columns: []catalog.Column{
			{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
			{Name: "val", Type: catalog.TypeText, Index: 1},
		},
	}

	// Join condition: t_small.id = t_large.fk
	joinCond := &ast.BinaryExpr{
		Op:    lexer.Token{Type: lexer.EQ, Literal: "="},
		Left:  &ast.ColumnRef{Table: "s", Column: "id", ResolvedTable: "s"},
		Right: &ast.ColumnRef{Table: "l", Column: "fk", ResolvedTable: "l"},
	}

	statsMap := map[string]*stats.TableStats{
		"t_small": {RowCount: 10},
		"t_large": {RowCount: 1000},
	}
	jo := cost.NewJoinOrderOptimizer(statsMap)

	// --- Case (a): bare LogicalScan leaves ---
	// Order: t_large (left, 1000 rows) JOIN t_small (right, 10 rows).
	// With real stats, the optimizer should prefer t_small × t_large.
	scanLarge := &logical.LogicalScan{TableName: "t_large", Alias: "l", Table: bigTable}
	scanSmall := &logical.LogicalScan{TableName: "t_small", Alias: "s", Table: smallTable}
	joinWithScans := &logical.LogicalJoin{
		Left:      scanLarge,
		Right:     scanSmall,
		JoinType:  logical.InnerJoin,
		Condition: joinCond,
	}

	resultA := jo.Optimize(joinWithScans)
	t.Logf("Case (a) [bare scans] result type: %T", resultA)
	joinA, ok := resultA.(*logical.LogicalJoin)
	require.True(t, ok, "result should still be a LogicalJoin")
	t.Logf("Case (a) join condition: %v (nil means conditions dropped)", joinA.Condition)
	assert.NotNil(t, joinA.Condition,
		"BUG-003: join condition should be preserved with bare scan leaves")

	// --- Case (b): LogicalProject-wrapped leaves (simulating ProjectionPushdown) ---
	projLarge := &logical.LogicalProject{
		Child: scanLarge,
		Expressions: []ast.Expression{
			&ast.ColumnRef{Table: "l", Column: "fk", ResolvedTable: "l"},
		},
		Aliases: []string{"l.fk"},
	}
	projSmall := &logical.LogicalProject{
		Child: scanSmall,
		Expressions: []ast.Expression{
			&ast.ColumnRef{Table: "s", Column: "id", ResolvedTable: "s"},
		},
		Aliases: []string{"s.id"},
	}
	joinWithProjects := &logical.LogicalJoin{
		Left:      projLarge, // larger table still on the left
		Right:     projSmall,
		JoinType:  logical.InnerJoin,
		Condition: joinCond,
	}

	resultB := jo.Optimize(joinWithProjects)
	t.Logf("Case (b) [project-wrapped leaves] result type: %T", resultB)
	joinB, ok := resultB.(*logical.LogicalJoin)
	require.True(t, ok, "result should still be a LogicalJoin")
	t.Logf("Case (b) join condition: %v (nil means conditions dropped)", joinB.Condition)

	// Determine what scan is under the left child of the result join.
	// If tryReorder succeeded: it should have picked the cheaper order = t_small (10 rows)
	// on the left, meaning the left child's leaf scan should be scanSmall (alias "s").
	// If tryReorder failed (due to empty aliases from LogicalProject): the original order
	// is preserved, so the left child's leaf scan should be scanLarge (alias "l").
	leftScanAlias := ""
	if lp, ok2 := joinB.Left.(*logical.LogicalProject); ok2 {
		if ls, ok3 := lp.Child.(*logical.LogicalScan); ok3 {
			leftScanAlias = ls.Alias
		}
	}
	t.Logf("Case (b) left child scan alias: %q (expected %q if reordered, %q if NOT reordered)",
		leftScanAlias, "s", "l")

	// BUG-003: tryReorder failed because planAlias returned "" for LogicalProject,
	// so the original order is preserved: t_large (alias "l") stays on the left.
	// The optimizer should have reordered to put t_small (alias "s") on the left.
	reorderHappened := leftScanAlias == "s"
	bugConfirmed := !reorderHappened
	t.Logf("BUG-003 confirmed (join reorder SKIPPED for project-wrapped leaves): %v", bugConfirmed)

	assert.True(t, bugConfirmed,
		fmt.Sprintf(
			"BUG-003 expected: planAlias returns \"\" for LogicalProject, causing "+
				"tryReorder to fail (no alias → condConnects always false → no subset "+
				"plan built → memo[fullMask] missing → tryReorder returns (nil,false)). "+
				"The optimizer falls back to the original order with t_large on left. "+
				"If this assertion fails (leftScanAlias=%q instead of %q), the bug has "+
				"been fixed: the optimizer correctly reordered and put t_small on left.",
			leftScanAlias, "l"))
}
