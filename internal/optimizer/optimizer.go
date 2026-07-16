package optimizer

import (
	"github.com/query-engine/query-engine/internal/optimizer/cost"
	"github.com/query-engine/query-engine/internal/optimizer/rule"
	"github.com/query-engine/query-engine/internal/planner/logical"
	"github.com/query-engine/query-engine/internal/stats"
)

const maxIterations = 10

// Optimizer orchestrates rule-based optimization.
type Optimizer struct {
	rules []rule.Rule
}

// New creates an optimizer with the default rule set.
func New() *Optimizer {
	return &Optimizer{rules: rule.DefaultRules()}
}

// NewWithRules creates an optimizer with a custom rule set.
func NewWithRules(rules []rule.Rule) *Optimizer {
	return &Optimizer{rules: rules}
}

// Optimize applies rules until fixed point (or maxIterations).
// Steps are appended to the provided slice.
func (o *Optimizer) Optimize(plan logical.Plan, steps *[]rule.OptimizationStep) logical.Plan {
	current := plan

	for iter := 0; iter < maxIterations; iter++ {
		anyChanged := false
		for _, r := range o.rules {
			newPlan, changed := r.Apply(current)
			step := rule.OptimizationStep{
				Rule:    r.Name(),
				Applied: changed,
			}
			if changed {
				step.Description = "Rule modified the plan"
				anyChanged = true
				current = newPlan
			} else {
				step.Description = "Rule did not apply"
			}
			if steps != nil {
				*steps = append(*steps, step)
			}
		}
		if !anyChanged {
			break
		}
	}

	return current
}

// OptimizeWithCBO runs RBO then applies CBO join-order optimization using table statistics.
func (o *Optimizer) OptimizeWithCBO(plan logical.Plan, statsMap map[string]*stats.TableStats, steps *[]rule.OptimizationStep) logical.Plan {
	plan = o.Optimize(plan, steps)
	if len(statsMap) > 0 {
		jo := cost.NewJoinOrderOptimizer(statsMap)
		plan = jo.Optimize(plan)
	}
	return plan
}
