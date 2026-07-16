package rule

import (
	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/planner/logical"
)

// PredicatePushdown pushes Filter nodes as deep as possible in the tree.
// It splits AND predicates and pushes each one independently.
type PredicatePushdown struct{}

func (r *PredicatePushdown) Name() string { return "PredicatePushdown" }

func (r *PredicatePushdown) Apply(plan logical.Plan) (logical.Plan, bool) {
	result, changed := r.push(plan, nil)
	return result, changed
}

// push recursively tries to push predicates. pendingPreds are predicates
// from ancestor Filter nodes that haven't been placed yet.
func (r *PredicatePushdown) push(plan logical.Plan, pendingPreds []ast.Expression) (logical.Plan, bool) {
	switch n := plan.(type) {
	case *logical.LogicalFilter:
		// Split this filter into individual conjuncts and add to pending
		preds := splitConjuncts(n.Predicate)
		allPreds := append(pendingPreds, preds...)
		return r.push(n.Child, allPreds)

	case *logical.LogicalJoin:
		// Only push through INNER joins (not outer joins — could lose null-extended rows)
		if n.JoinType != logical.InnerJoin {
			// Re-attach pending as filter above join
			child, _ := r.pushDown(plan, nil)
			return wrapWithPending(child, pendingPreds)
		}

		leftTables := schemaTableSet(n.Left.Schema())
		rightTables := schemaTableSet(n.Right.Schema())

		var leftPreds, rightPreds, remaining []ast.Expression
		for _, pred := range pendingPreds {
			if canSubsume(pred, leftTables) {
				leftPreds = append(leftPreds, pred)
			} else if canSubsume(pred, rightTables) {
				rightPreds = append(rightPreds, pred)
			} else {
				remaining = append(remaining, pred)
			}
		}

		newLeft, _ := r.push(n.Left, leftPreds)
		newRight, _ := r.push(n.Right, rightPreds)

		changed := newLeft != n.Left || newRight != n.Right || len(leftPreds) > 0 || len(rightPreds) > 0

		newJoin := &logical.LogicalJoin{
			Left:      newLeft,
			Right:     newRight,
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}

		result := logical.Plan(newJoin)
		if len(remaining) > 0 {
			result = &logical.LogicalFilter{Child: newJoin, Predicate: joinConjuncts(remaining)}
		}
		return result, changed || len(pendingPreds) > 0

	case *logical.LogicalAggregate:
		// Don't push predicates through aggregation (semantics change)
		newChild, childChanged := r.push(n.Child, nil)
		newAgg := &logical.LogicalAggregate{
			Child:   newChild,
			GroupBy: n.GroupBy,
			Aggs:    n.Aggs,
		}
		result, wrapChanged := wrapWithPending(newAgg, pendingPreds)
		return result, childChanged || wrapChanged

	case *logical.LogicalProject:
		// Push through projection
		newChild, childChanged := r.push(n.Child, nil)
		newProj := &logical.LogicalProject{
			Child:       newChild,
			Expressions: n.Expressions,
			Aliases:     n.Aliases,
		}
		result, wrapChanged := wrapWithPending(newProj, pendingPreds)
		return result, childChanged || wrapChanged

	case *logical.LogicalScan:
		// Base case: attach remaining predicates as a filter on the scan
		return wrapWithPending(n, pendingPreds)

	default:
		// Recurse into children
		result, changed := r.pushDown(plan, pendingPreds)
		return result, changed
	}
}

// pushDown handles generic plans by recursing into children.
func (r *PredicatePushdown) pushDown(plan logical.Plan, pendingPreds []ast.Expression) (logical.Plan, bool) {
	if len(pendingPreds) > 0 {
		return wrapWithPending(plan, pendingPreds)
	}
	children := plan.Children()
	if len(children) == 0 {
		return plan, false
	}
	changed := false
	newChildren := make([]logical.Plan, len(children))
	for i, child := range children {
		newChild, c := r.push(child, nil)
		newChildren[i] = newChild
		changed = changed || c
	}
	if !changed {
		return plan, false
	}
	return rebuildNode(plan, newChildren), true
}

// wrapWithPending wraps a plan with a Filter containing all pending predicates.
func wrapWithPending(plan logical.Plan, preds []ast.Expression) (logical.Plan, bool) {
	if len(preds) == 0 {
		return plan, false
	}
	return &logical.LogicalFilter{
		Child:     plan,
		Predicate: joinConjuncts(preds),
	}, true
}
