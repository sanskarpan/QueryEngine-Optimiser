package rule

import (
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/planner/logical"
)

// ProjectionPushdown eliminates unused columns by inserting narrowing projections.
// This reduces tuple width flowing through joins.
type ProjectionPushdown struct{}

func (r *ProjectionPushdown) Name() string { return "ProjectionPushdown" }

func (r *ProjectionPushdown) Apply(plan logical.Plan) (logical.Plan, bool) {
	// Collect required columns from the root, then push down
	required := collectRequired(plan)
	result, changed := r.pushdown(plan, required)
	return result, changed
}

// collectRequired walks the entire plan tree and collects ALL referenced column names.
func collectRequired(plan logical.Plan) map[string]bool {
	required := make(map[string]bool)
	walkForColumns(plan, required)
	return required
}

func walkForColumns(plan logical.Plan, required map[string]bool) {
	if plan == nil {
		return
	}
	switch n := plan.(type) {
	case *logical.LogicalFilter:
		collectFromExpr(n.Predicate, required)
		walkForColumns(n.Child, required)
	case *logical.LogicalProject:
		for _, e := range n.Expressions {
			collectFromExpr(e, required)
		}
		walkForColumns(n.Child, required)
	case *logical.LogicalJoin:
		collectFromExpr(n.Condition, required)
		walkForColumns(n.Left, required)
		walkForColumns(n.Right, required)
	case *logical.LogicalAggregate:
		for _, g := range n.GroupBy {
			collectFromExpr(g, required)
		}
		for _, agg := range n.Aggs {
			collectFromExpr(agg.Arg, required)
		}
		walkForColumns(n.Child, required)
	case *logical.LogicalSort:
		for _, s := range n.SortSpecs {
			collectFromExpr(s.Expr, required)
		}
		walkForColumns(n.Child, required)
	case *logical.LogicalLimit:
		walkForColumns(n.Child, required)
	default:
		for _, child := range plan.Children() {
			walkForColumns(child, required)
		}
	}
}

func collectFromExpr(expr ast.Expression, required map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.ColumnRef:
		// Always record the bare column name so pushdown doesn't prune columns
		// that are needed but whose schema entry lacks a table prefix.
		required[e.Column] = true
		if e.ResolvedTable != "" {
			required[e.ResolvedTable+"."+e.Column] = true
		}
	case *ast.BinaryExpr:
		collectFromExpr(e.Left, required)
		collectFromExpr(e.Right, required)
	case *ast.UnaryExpr:
		collectFromExpr(e.Expr, required)
	case *ast.AliasExpr:
		collectFromExpr(e.Expr, required)
	case *ast.FunctionCall:
		for _, arg := range e.Args {
			collectFromExpr(arg, required)
		}
	case *ast.IsNullExpr:
		collectFromExpr(e.Expr, required)
	case *ast.BetweenExpr:
		collectFromExpr(e.Expr, required)
		collectFromExpr(e.Low, required)
		collectFromExpr(e.High, required)
	case *ast.InExpr:
		collectFromExpr(e.Expr, required)
		for _, item := range e.List {
			collectFromExpr(item, required)
		}
	}
}

// pushdown inserts narrowing projection nodes below joins when many columns are unused.
func (r *ProjectionPushdown) pushdown(plan logical.Plan, required map[string]bool) (logical.Plan, bool) {
	switch n := plan.(type) {
	case *logical.LogicalJoin:
		// Determine which columns from left/right are actually needed
		leftSchema := n.Left.Schema()
		rightSchema := n.Right.Schema()

		leftNeeded := neededFromSchema(leftSchema, required)
		rightNeeded := neededFromSchema(rightSchema, required)

		newLeft, lc := r.pushdown(n.Left, required)
		newRight, rc := r.pushdown(n.Right, required)

		changed := lc || rc

		// Insert narrow projections if we can drop columns
		// Only insert if it actually reduces columns (avoid no-op projections)
		if len(leftNeeded) < len(leftSchema) && len(leftNeeded) > 0 {
			exprs, aliases := schemaToExpressions(leftNeeded)
			newLeft = &logical.LogicalProject{Child: newLeft, Expressions: exprs, Aliases: aliases}
			changed = true
		}
		if len(rightNeeded) < len(rightSchema) && len(rightNeeded) > 0 {
			exprs, aliases := schemaToExpressions(rightNeeded)
			newRight = &logical.LogicalProject{Child: newRight, Expressions: exprs, Aliases: aliases}
			changed = true
		}

		if !changed {
			return plan, false
		}
		return &logical.LogicalJoin{
			Left:      newLeft,
			Right:     newRight,
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}, true

	default:
		// Recurse into children
		children := plan.Children()
		if len(children) == 0 {
			return plan, false
		}
		changed := false
		newChildren := make([]logical.Plan, len(children))
		for i, child := range children {
			nc, c := r.pushdown(child, required)
			newChildren[i] = nc
			changed = changed || c
		}
		if !changed {
			return plan, false
		}
		return rebuildNode(plan, newChildren), true
	}
}

func neededFromSchema(schema []catalog.Column, required map[string]bool) []catalog.Column {
	var needed []catalog.Column
	for _, col := range schema {
		if required[col.Name] {
			needed = append(needed, col)
			continue
		}
		// Also keep join columns (in case they're needed for join condition)
		// Heuristic: always keep PK columns and columns that appear in required by short name
		parts := strings.SplitN(col.Name, ".", 2)
		if len(parts) == 2 && required[parts[1]] {
			needed = append(needed, col)
		}
	}
	return needed
}

func schemaToExpressions(schema []catalog.Column) ([]ast.Expression, []string) {
	exprs := make([]ast.Expression, len(schema))
	aliases := make([]string, len(schema))
	for i, col := range schema {
		parts := strings.SplitN(col.Name, ".", 2)
		ref := &ast.ColumnRef{}
		if len(parts) == 2 {
			ref.Table = parts[0]
			ref.Column = parts[1]
			ref.ResolvedTable = parts[0]
		} else {
			ref.Column = col.Name
		}
		exprs[i] = ref
		aliases[i] = col.Name
	}
	return exprs, aliases
}
