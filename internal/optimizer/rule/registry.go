package rule

// DefaultRules returns the default ordered list of optimization rules.
func DefaultRules() []Rule {
	return []Rule{
		&PredicatePushdown{},
		&ConstantFolding{},
		&EliminateDeadFilter{},
		&ProjectionPushdown{},
	}
}
