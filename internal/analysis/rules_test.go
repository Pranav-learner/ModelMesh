package analysis

import "testing"

func TestRuleSet_WithIsImmutable(t *testing.T) {
	base := DefaultRuleSet()
	baseLen := len(base.Rules)

	custom := base.With(Rule{
		Name: "always", Description: "test rule", Weight: 10,
		Match: func(Signals) bool { return true },
	})
	if len(base.Rules) != baseLen {
		t.Errorf("With mutated the base rule set: %d, want %d", len(base.Rules), baseLen)
	}
	if len(custom.Rules) != baseLen+1 {
		t.Errorf("extended set has %d rules, want %d", len(custom.Rules), baseLen+1)
	}
}

func TestRuleEngine_CustomRuleSet(t *testing.T) {
	// A brand-new rule set with a single domain rule.
	rs := RuleSet{Name: "custom", Rules: []Rule{
		{
			Name: "mentions_sql", Description: "prompt mentions SQL", Weight: 5,
			Features: []string{"has_structured_data"},
			Match:    func(s Signals) bool { return s.HasStructuredData },
		},
	}}
	c := NewRuleClassifier(ClassifierConfig{RuleSet: rs, MediumThreshold: 1, ComplexThreshold: 4})

	got := c.Classify(Signals{HasStructuredData: true})
	if got.Complexity != ComplexityComplex {
		t.Errorf("custom rule (weight 5) → %s, want complex", got.Complexity)
	}
	if len(got.TriggeredRules) != 1 || got.TriggeredRules[0].Name != "mentions_sql" {
		t.Errorf("triggered rules = %+v, want mentions_sql", got.TriggeredRules)
	}

	// A prompt the rule does not match is Simple.
	none := c.Classify(Signals{HasCode: true})
	if none.Complexity != ComplexitySimple || len(none.TriggeredRules) != 0 {
		t.Errorf("non-matching prompt = %s (%d rules), want simple/0", none.Complexity, len(none.TriggeredRules))
	}
}

func TestRuleEngine_FeaturesUsedDeduplicated(t *testing.T) {
	c := NewRuleClassifier(DefaultClassifierConfig())
	// Both instruction rules fire and both list "instruction_count".
	got := c.Classify(Signals{InstructionCount: 6})
	seen := map[string]int{}
	for _, f := range got.FeaturesUsed {
		seen[f]++
	}
	if seen["instruction_count"] != 1 {
		t.Errorf("instruction_count listed %d times, want 1 (deduped)", seen["instruction_count"])
	}
}
