package cost

import (
	"math"
	"strings"

	"github.com/query-engine/query-engine/internal/ast"
	"github.com/query-engine/query-engine/internal/lexer"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/stats"
)

const maxJoinRelations = 10

// JoinOrderOptimizer reorders inner joins using dynamic programming.
type JoinOrderOptimizer struct {
	est *Estimator
}

// NewJoinOrderOptimizer creates a join order optimizer.
func NewJoinOrderOptimizer(statsMap map[string]*stats.TableStats) *JoinOrderOptimizer {
	return &JoinOrderOptimizer{est: NewEstimator(statsMap)}
}

// Optimize walks the plan and reorders any chains of inner joins it finds.
// Other plan nodes are left unchanged.
func (jo *JoinOrderOptimizer) Optimize(plan logical.Plan) logical.Plan {
	return jo.optimizeNode(plan)
}

func (jo *JoinOrderOptimizer) optimizeNode(plan logical.Plan) logical.Plan {
	switch n := plan.(type) {
	case *logical.LogicalJoin:
		// Try to extract a flat join tree and reorder it.
		if reordered, ok := jo.tryReorder(n); ok {
			return reordered
		}
		// Could not reorder — recurse into children individually.
		return &logical.LogicalJoin{
			Left:      jo.optimizeNode(n.Left),
			Right:     jo.optimizeNode(n.Right),
			JoinType:  n.JoinType,
			Condition: n.Condition,
		}
	case *logical.LogicalFilter:
		return &logical.LogicalFilter{Child: jo.optimizeNode(n.Child), Predicate: n.Predicate}
	case *logical.LogicalProject:
		return &logical.LogicalProject{Child: jo.optimizeNode(n.Child), Expressions: n.Expressions, Aliases: n.Aliases}
	case *logical.LogicalAggregate:
		return &logical.LogicalAggregate{Child: jo.optimizeNode(n.Child), GroupBy: n.GroupBy, Aggs: n.Aggs}
	case *logical.LogicalSort:
		return &logical.LogicalSort{Child: jo.optimizeNode(n.Child), SortSpecs: n.SortSpecs}
	case *logical.LogicalLimit:
		return &logical.LogicalLimit{Child: jo.optimizeNode(n.Child), Count: n.Count, Offset: n.Offset}
	default:
		return plan
	}
}

// relation is a leaf plan in the join tree with its alias.
type relation struct {
	plan  logical.Plan
	alias string
}

// condEntry holds a join condition with the table aliases it references.
type condEntry struct {
	expr   ast.Expression
	tables map[string]bool
}

// memoEntry is the best plan found for a given subset of relations.
type memoEntry struct {
	plan logical.Plan
	rows int64
	cost float64
}

// tryReorder extracts a flat join from the plan tree and applies DP reordering.
// Returns (reordered plan, true) on success; (nil, false) if reordering is not applicable.
func (jo *JoinOrderOptimizer) tryReorder(root *logical.LogicalJoin) (logical.Plan, bool) {
	var rels []relation
	var conds []condEntry
	if !extractInnerJoins(root, &rels, &conds) {
		return nil, false
	}
	n := len(rels)
	if n < 2 || n > maxJoinRelations {
		return nil, false
	}

	memo := make(map[uint32]memoEntry)

	// Base case: single relations.
	for i, rel := range rels {
		mask := uint32(1) << i
		rows := jo.est.EstimateRows(rel.plan)
		cost := jo.est.EstimateCost(rel.plan)
		memo[mask] = memoEntry{plan: rel.plan, rows: rows, cost: cost}
	}

	fullMask := uint32((1 << n) - 1)

	// Fill subsets of increasing size.
	for size := 2; size <= n; size++ {
		iterSubsets(fullMask, size, func(mask uint32) {
			best := memoEntry{cost: math.MaxFloat64}
			// Enumerate all non-empty proper subsets of mask as the left side.
			for left := (mask - 1) & mask; left > 0; left = (left - 1) & mask {
				right := mask ^ left
				if right == 0 {
					continue
				}
				leftEntry, lok := memo[left]
				rightEntry, rok := memo[right]
				if !lok || !rok {
					continue
				}
				// Find conditions that connect left and right.
				cond := connectingCondition(left, right, rels, conds)
				if cond == nil && !allRelationsConnected(left, right, conds, rels) {
					// Allow cross joins only if no conditions exist at all (rare).
					if len(conds) > 0 {
						continue
					}
				}
				joinRows := innerJoinRows(leftEntry.rows, rightEntry.rows, cond, leftEntry.plan, rightEntry.plan, jo.est.stats)
				joinCost := leftEntry.cost + rightEntry.cost + jo.est.model.HashJoin(leftEntry.rows, rightEntry.rows)
				if joinCost < best.cost {
					best = memoEntry{
						plan: &logical.LogicalJoin{
							Left:      leftEntry.plan,
							Right:     rightEntry.plan,
							JoinType:  logical.InnerJoin,
							Condition: cond,
						},
						rows: joinRows,
						cost: joinCost,
					}
				}
			}
			if best.cost < math.MaxFloat64 {
				memo[mask] = best
			}
		})
	}

	entry, ok := memo[fullMask]
	if !ok {
		return nil, false
	}
	return entry.plan, true
}

// extractInnerJoins recursively flattens a chain of inner joins into relations and conditions.
// Returns false if any non-inner join is encountered.
func extractInnerJoins(plan logical.Plan, rels *[]relation, conds *[]condEntry) bool {
	join, ok := plan.(*logical.LogicalJoin)
	if !ok {
		// Leaf relation — use the plan's alias.
		alias := planAlias(plan)
		*rels = append(*rels, relation{plan: plan, alias: alias})
		return true
	}
	if join.JoinType != logical.InnerJoin {
		// Non-inner join: treat the entire subtree as a leaf.
		alias := planAlias(plan)
		*rels = append(*rels, relation{plan: plan, alias: alias})
		return true
	}
	// Recurse into both sides.
	if !extractInnerJoins(join.Left, rels, conds) {
		return false
	}
	if !extractInnerJoins(join.Right, rels, conds) {
		return false
	}
	// Record the join condition.
	if join.Condition != nil {
		*conds = append(*conds, condEntry{
			expr:   join.Condition,
			tables: exprTableRefs(join.Condition),
		})
	}
	return true
}

// planAlias returns the primary alias for a plan node.
func planAlias(plan logical.Plan) string {
	switch n := plan.(type) {
	case *logical.LogicalScan:
		return strings.ToLower(n.Alias)
	case *logical.LogicalFilter:
		return planAlias(n.Child)
	case *logical.LogicalSubquery:
		return strings.ToLower(n.Alias)
	}
	return ""
}

// connectingCondition returns a single condition (or AND of conditions) that
// connects the given left and right relation subsets, or nil if none exists.
func connectingCondition(left, right uint32, rels []relation, conds []condEntry) ast.Expression {
	var parts []ast.Expression
	for _, c := range conds {
		if condConnects(c, left, right, rels) {
			parts = append(parts, c.expr)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return joinExprs(parts)
}

// allRelationsConnected returns true if there is at least one condition that
// references tables on both sides (used as a gate to avoid unconstrained cross joins).
func allRelationsConnected(left, right uint32, conds []condEntry, rels []relation) bool {
	for _, c := range conds {
		if condConnects(c, left, right, rels) {
			return true
		}
	}
	return false
}

// condConnects returns true if the condition references at least one table alias
// from the left mask and at least one from the right mask.
func condConnects(c condEntry, left, right uint32, rels []relation) bool {
	leftAliases := maskAliases(left, rels)
	rightAliases := maskAliases(right, rels)
	hasLeft, hasRight := false, false
	for tbl := range c.tables {
		if leftAliases[tbl] {
			hasLeft = true
		}
		if rightAliases[tbl] {
			hasRight = true
		}
	}
	return hasLeft && hasRight
}

// maskAliases returns a set of lowercase aliases for the relations in the mask.
func maskAliases(mask uint32, rels []relation) map[string]bool {
	out := make(map[string]bool)
	for i, rel := range rels {
		if mask&(uint32(1)<<i) != 0 && rel.alias != "" {
			out[rel.alias] = true
		}
	}
	return out
}

// exprTableRefs collects all table aliases referenced by ColumnRef nodes in an expression.
func exprTableRefs(expr ast.Expression) map[string]bool {
	tables := make(map[string]bool)
	walkExprTables(expr, tables)
	return tables
}

func walkExprTables(expr ast.Expression, out map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.ColumnRef:
		if e.Table != "" {
			out[strings.ToLower(e.Table)] = true
		}
		if e.ResolvedTable != "" {
			out[strings.ToLower(e.ResolvedTable)] = true
		}
	case *ast.BinaryExpr:
		walkExprTables(e.Left, out)
		walkExprTables(e.Right, out)
	case *ast.UnaryExpr:
		walkExprTables(e.Expr, out)
	}
}

// joinExprs combines a slice of expressions with AND.
func joinExprs(exprs []ast.Expression) ast.Expression {
	if len(exprs) == 0 {
		return nil
	}
	result := exprs[0]
	for _, e := range exprs[1:] {
		result = &ast.BinaryExpr{
			Op:    lexer.Token{Type: lexer.AND, Literal: "AND"},
			Left:  result,
			Right: e,
		}
	}
	return result
}

// iterSubsets calls fn for every subset of mask that has exactly size bits set.
func iterSubsets(mask uint32, size int, fn func(uint32)) {
	// Gosper's hack for iterating subsets of a given popcount.
	if size == 0 {
		fn(0)
		return
	}
	// Start with the lowest size-bit subset of mask.
	sub := lowestKBits(mask, size)
	for sub != 0 && sub <= mask {
		if sub&mask == sub && popcount32(sub) == size {
			fn(sub)
		}
		// Next subset of mask with same popcount (Gosper's hack adapted to mask).
		sub = nextSubset(sub, mask)
		if sub == 0 {
			break
		}
	}
}

// nextSubset returns the next subset of universe with the same popcount, or 0 if done.
func nextSubset(s, universe uint32) uint32 {
	if s == 0 {
		return 0
	}
	// Gosper's hack.
	c := s & (^s + 1)
	r := s + c
	next := (((r ^ s) >> 2) / c) | r
	// Constrain to universe.
	if next > universe {
		return 0
	}
	for next != 0 && next <= universe {
		if next&universe == next && popcount32(next) == popcount32(s) {
			return next
		}
		next++
	}
	return 0
}

// lowestKBits returns the lowest k set bits of mask.
func lowestKBits(mask uint32, k int) uint32 {
	result := uint32(0)
	count := 0
	for i := 0; i < 32 && count < k; i++ {
		if mask&(1<<i) != 0 {
			result |= 1 << i
			count++
		}
	}
	return result
}

// popcount32 counts set bits in a uint32.
func popcount32(x uint32) int {
	count := 0
	for x != 0 {
		count += int(x & 1)
		x >>= 1
	}
	return count
}
