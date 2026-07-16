// Package rule provides the optimizer rule interface and all built-in optimization rules.
// Rules transform logical plan trees: constant folding eliminates dead branches,
// predicate pushdown moves filters closer to scans, and projection pushdown
// reduces column width early in the pipeline.
package rule

import "github.com/query-engine/query-engine/internal/planner/logical"

// Rule is an optimizer transformation rule.
type Rule interface {
	// Name returns the rule's display name.
	Name() string
	// Apply attempts to transform the plan. Returns (newPlan, changed).
	Apply(plan logical.Plan) (logical.Plan, bool)
}

// OptimizationStep records a single rule application attempt.
type OptimizationStep struct {
	Rule        string
	Applied     bool
	Description string
}
