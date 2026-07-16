package rule

import (
	"fmt"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/catalog"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/logical"
)

// ConstantFolding evaluates constant expressions at plan time.
type ConstantFolding struct{}

func (r *ConstantFolding) Name() string { return "ConstantFolding" }

func (r *ConstantFolding) Apply(plan logical.Plan) (logical.Plan, bool) {
	return applyToTree(plan, r.foldNode)
}

func (r *ConstantFolding) foldNode(plan logical.Plan) (logical.Plan, bool) {
	switch n := plan.(type) {
	case *logical.LogicalFilter:
		folded, foldedExpr := foldExpr(n.Predicate)
		if folded {
			return &logical.LogicalFilter{Child: n.Child, Predicate: foldedExpr}, true
		}
	case *logical.LogicalProject:
		changed := false
		newExprs := make([]ast.Expression, len(n.Expressions))
		for i, e := range n.Expressions {
			folded, fe := foldExpr(e)
			if folded {
				changed = true
				newExprs[i] = fe
			} else {
				newExprs[i] = e
			}
		}
		if changed {
			return &logical.LogicalProject{Child: n.Child, Expressions: newExprs, Aliases: n.Aliases}, true
		}
	}
	return plan, false
}

// foldExpr attempts to constant-fold an expression.
// Returns (changed, newExpr).
func foldExpr(expr ast.Expression) (bool, ast.Expression) {
	if expr == nil {
		return false, nil
	}
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		changedL, left := foldExpr(e.Left)
		changedR, right := foldExpr(e.Right)

		if isLiteral(left) && isLiteral(right) {
			result := evalConstBinary(e.Op, left, right)
			if result != nil {
				return true, result
			}
		}

		// Simplify x AND TRUE → x, x OR FALSE → x, NOT NOT x → x
		switch e.Op.Type {
		case lexer.AND:
			if isBoolLit(left, true) {
				return true, right
			}
			if isBoolLit(right, true) {
				return true, left
			}
			if isBoolLit(left, false) {
				return true, &ast.BoolLiteral{Value: false}
			}
			if isBoolLit(right, false) {
				return true, &ast.BoolLiteral{Value: false}
			}
		case lexer.OR:
			if isBoolLit(left, false) {
				return true, right
			}
			if isBoolLit(right, false) {
				return true, left
			}
			if isBoolLit(left, true) {
				return true, &ast.BoolLiteral{Value: true}
			}
			if isBoolLit(right, true) {
				return true, &ast.BoolLiteral{Value: true}
			}
		}

		if changedL || changedR {
			return true, &ast.BinaryExpr{Pos: e.Pos, Left: left, Op: e.Op, Right: right}
		}

	case *ast.UnaryExpr:
		changed, inner := foldExpr(e.Expr)
		// NOT NOT x → x
		if e.Op.Type == lexer.NOT {
			if un, ok := inner.(*ast.UnaryExpr); ok && un.Op.Type == lexer.NOT {
				return true, un.Expr
			}
			if b, ok := inner.(*ast.BoolLiteral); ok {
				return true, &ast.BoolLiteral{Pos: e.Pos, Value: !b.Value}
			}
		}
		// Unary minus on literal
		if e.Op.Type == lexer.MINUS {
			if i, ok := inner.(*ast.IntLiteral); ok {
				return true, &ast.IntLiteral{Pos: e.Pos, Value: -i.Value}
			}
			if f, ok := inner.(*ast.FloatLiteral); ok {
				return true, &ast.FloatLiteral{Pos: e.Pos, Value: -f.Value}
			}
		}
		if changed {
			return true, &ast.UnaryExpr{Pos: e.Pos, Op: e.Op, Expr: inner}
		}

	case *ast.CaseExpr:
		// Fold sub-expressions first.
		changed := false
		chOp, newOperand := foldExpr(e.Operand)
		changed = changed || chOp

		newWhens := make([]ast.WhenClause, 0, len(e.Whens))
		for _, w := range e.Whens {
			chC, newCond := foldExpr(w.Condition)
			chR, newResult := foldExpr(w.Result)
			if chC || chR {
				changed = true
			}
			newWhens = append(newWhens, ast.WhenClause{Condition: newCond, Result: newResult})
		}
		chEl, newElse := foldExpr(e.ElseExpr)
		if chEl {
			changed = true
		}

		// For searched CASE (no operand): statically evaluate leading constant conditions.
		if e.Operand == nil {
			remaining := newWhens[:0:0]
			for _, w := range newWhens {
				if isBoolLit(w.Condition, true) {
					return true, w.Result // this branch always fires
				}
				if isBoolLit(w.Condition, false) {
					changed = true
					continue // dead branch — skip
				}
				remaining = append(remaining, w)
			}
			if len(remaining) == 0 {
				// All branches were constant-false; reduce to ELSE or NULL.
				if newElse != nil {
					return true, newElse
				}
				return true, &ast.NullLiteral{}
			}
			newWhens = remaining
		}

		if changed {
			return true, &ast.CaseExpr{Pos: e.Pos, Operand: newOperand, Whens: newWhens, ElseExpr: newElse}
		}

	case *ast.AliasExpr:
		changed, inner := foldExpr(e.Expr)
		if changed {
			return true, &ast.AliasExpr{Pos: e.Pos, Expr: inner, Alias: e.Alias}
		}
	}

	return false, expr
}

func isLiteral(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.IntLiteral, *ast.FloatLiteral, *ast.StringLiteral, *ast.BoolLiteral, *ast.NullLiteral:
		return true
	}
	return false
}

func isBoolLit(expr ast.Expression, val bool) bool {
	if b, ok := expr.(*ast.BoolLiteral); ok {
		return b.Value == val
	}
	return false
}

func evalConstBinary(op lexer.Token, left, right ast.Expression) ast.Expression {
	lv := litToValue(left)
	rv := litToValue(right)

	switch op.Type {
	case lexer.PLUS:
		r, err := lv.Add(rv)
		if err != nil {
			return nil
		}
		return valueToLit(r)
	case lexer.MINUS:
		r, err := lv.Sub(rv)
		if err != nil {
			return nil
		}
		return valueToLit(r)
	case lexer.STAR:
		r, err := lv.Mul(rv)
		if err != nil {
			return nil
		}
		return valueToLit(r)
	case lexer.SLASH:
		r, err := lv.Div(rv)
		if err != nil {
			return nil
		}
		return valueToLit(r)
	case lexer.EQ, lexer.NEQ, lexer.LT, lexer.GT, lexer.LTE, lexer.GTE:
		cmp, err := lv.Compare(rv)
		if err != nil {
			return nil
		}
		var result bool
		switch op.Type {
		case lexer.EQ:
			result = cmp == 0
		case lexer.NEQ:
			result = cmp != 0
		case lexer.LT:
			result = cmp < 0
		case lexer.GT:
			result = cmp > 0
		case lexer.LTE:
			result = cmp <= 0
		case lexer.GTE:
			result = cmp >= 0
		}
		return &ast.BoolLiteral{Value: result}
	}
	return nil
}

func litToValue(expr ast.Expression) catalog.Value {
	switch e := expr.(type) {
	case *ast.IntLiteral:
		return catalog.IntValue(e.Value)
	case *ast.FloatLiteral:
		return catalog.FloatValue(e.Value)
	case *ast.StringLiteral:
		return catalog.TextValue(e.Value)
	case *ast.BoolLiteral:
		return catalog.BoolValue(e.Value)
	case *ast.NullLiteral:
		return catalog.NullValue()
	}
	return catalog.NullValue()
}

func valueToLit(v catalog.Value) ast.Expression {
	if v.IsNull {
		return &ast.NullLiteral{}
	}
	switch v.Type {
	case catalog.TypeInt:
		return &ast.IntLiteral{Value: v.IntVal}
	case catalog.TypeFloat:
		return &ast.FloatLiteral{Value: v.FloatVal}
	case catalog.TypeText:
		return &ast.StringLiteral{Value: v.StrVal}
	case catalog.TypeBool:
		return &ast.BoolLiteral{Value: v.BoolVal}
	}
	return &ast.NullLiteral{}
}

// EliminateDeadFilter removes Filter(TRUE) and replaces Filter(FALSE) with EmptyRelation.
type EliminateDeadFilter struct{}

func (r *EliminateDeadFilter) Name() string { return "EliminateDeadFilter" }

func (r *EliminateDeadFilter) Apply(plan logical.Plan) (logical.Plan, bool) {
	return applyToTree(plan, r.eliminate)
}

func (r *EliminateDeadFilter) eliminate(plan logical.Plan) (logical.Plan, bool) {
	f, ok := plan.(*logical.LogicalFilter)
	if !ok {
		return plan, false
	}
	if isBoolLit(f.Predicate, true) {
		return f.Child, true
	}
	if isBoolLit(f.Predicate, false) {
		return &logical.EmptyRelation{Cols: f.Schema()}, true
	}
	return plan, false
}

// applyToTree recursively applies a node-level transform to every node in the tree.
func applyToTree(plan logical.Plan, transform func(logical.Plan) (logical.Plan, bool)) (logical.Plan, bool) {
	// Apply to children first (bottom-up)
	children := plan.Children()
	if len(children) == 0 {
		return transform(plan)
	}

	changed := false
	newChildren := make([]logical.Plan, len(children))
	for i, child := range children {
		newChild, childChanged := applyToTree(child, transform)
		newChildren[i] = newChild
		changed = changed || childChanged
	}

	// Reconstruct node with new children if any changed
	var reconstructed logical.Plan
	if changed {
		reconstructed = rebuildNode(plan, newChildren)
	} else {
		reconstructed = plan
	}

	// Apply transform to this (potentially rebuilt) node
	result, nodeChanged := transform(reconstructed)
	return result, changed || nodeChanged
}

// rebuildNode creates a new node of the same type with updated children.
func rebuildNode(plan logical.Plan, children []logical.Plan) logical.Plan {
	switch n := plan.(type) {
	case *logical.LogicalFilter:
		return &logical.LogicalFilter{Child: children[0], Predicate: n.Predicate}
	case *logical.LogicalProject:
		return &logical.LogicalProject{Child: children[0], Expressions: n.Expressions, Aliases: n.Aliases}
	case *logical.LogicalJoin:
		return &logical.LogicalJoin{Left: children[0], Right: children[1], JoinType: n.JoinType, Condition: n.Condition}
	case *logical.LogicalAggregate:
		return &logical.LogicalAggregate{Child: children[0], GroupBy: n.GroupBy, Aggs: n.Aggs}
	case *logical.LogicalSort:
		return &logical.LogicalSort{Child: children[0], SortSpecs: n.SortSpecs}
	case *logical.LogicalLimit:
		return &logical.LogicalLimit{Child: children[0], Count: n.Count, Offset: n.Offset}
	case *logical.LogicalSubquery:
		return &logical.LogicalSubquery{Child: children[0], Alias: n.Alias}
	case *logical.LogicalDistinct:
		return &logical.LogicalDistinct{Child: children[0]}
	}
	return plan
}

// -----------------------------------------------------------------------
// Helpers (needed by predicate pushdown too)
// -----------------------------------------------------------------------

// splitConjuncts splits AND chains into individual predicates.
func splitConjuncts(expr ast.Expression) []ast.Expression {
	if b, ok := expr.(*ast.BinaryExpr); ok && b.Op.Type == lexer.AND {
		return append(splitConjuncts(b.Left), splitConjuncts(b.Right)...)
	}
	return []ast.Expression{expr}
}

// joinConjuncts merges predicates with AND.
func joinConjuncts(preds []ast.Expression) ast.Expression {
	if len(preds) == 0 {
		return &ast.BoolLiteral{Value: true}
	}
	result := preds[0]
	for _, p := range preds[1:] {
		result = &ast.BinaryExpr{
			Op:    lexer.Token{Type: lexer.AND, Literal: "AND"},
			Left:  result,
			Right: p,
		}
	}
	return result
}

// predicateTables returns the set of table aliases referenced in an expression.
func predicateTables(expr ast.Expression) map[string]bool {
	tables := make(map[string]bool)
	collectTables(expr, tables)
	return tables
}

func collectTables(expr ast.Expression, tables map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.ColumnRef:
		if e.ResolvedTable != "" {
			tables[e.ResolvedTable] = true
		}
	case *ast.BinaryExpr:
		collectTables(e.Left, tables)
		collectTables(e.Right, tables)
	case *ast.UnaryExpr:
		collectTables(e.Expr, tables)
	case *ast.FunctionCall:
		for _, arg := range e.Args {
			collectTables(arg, tables)
		}
	case *ast.AliasExpr:
		collectTables(e.Expr, tables)
	case *ast.IsNullExpr:
		collectTables(e.Expr, tables)
	case *ast.BetweenExpr:
		collectTables(e.Expr, tables)
		collectTables(e.Low, tables)
		collectTables(e.High, tables)
	case *ast.InExpr:
		collectTables(e.Expr, tables)
		for _, item := range e.List {
			collectTables(item, tables)
		}
	}
}

// schemaTableSet returns the set of table aliases in a schema.
func schemaTableSet(schema []catalog.Column) map[string]bool {
	tables := make(map[string]bool)
	for _, col := range schema {
		if idx := indexOfDot(col.Name); idx >= 0 {
			tables[col.Name[:idx]] = true
		}
	}
	return tables
}

func indexOfDot(s string) int {
	for i, c := range s {
		if c == '.' {
			return i
		}
	}
	return -1
}

// canSubsume returns true if all tables referenced by pred are in the given set.
func canSubsume(pred ast.Expression, availableTables map[string]bool) bool {
	for t := range predicateTables(pred) {
		if !availableTables[t] {
			return false
		}
	}
	return true
}

// Unused but needed to satisfy import
var _ = fmt.Sprintf
