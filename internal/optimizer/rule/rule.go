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
