package analysis

// Rule is a single configurable complexity heuristic. When Match fires on the
// signals, the rule contributes Weight complexity points and is recorded as
// triggered (with the Features it read, for explainability). Rules are pure
// functions of Signals — deterministic and side-effect free.
type Rule struct {
	Name        string
	Description string
	Weight      float64
	// Features lists the signal names this rule inspects, surfaced in the
	// explanation's "features used".
	Features []string
	// Match reports whether the rule applies to the signals.
	Match func(Signals) bool
}

// RuleSet is a named, ordered collection of rules — the unit that makes future
// rule sets easy to add, compose, and swap.
type RuleSet struct {
	Name  string
	Rules []Rule
}

// With returns a copy of the rule set with the given rules appended, so a caller
// can extend the defaults without mutating them.
func (rs RuleSet) With(rules ...Rule) RuleSet {
	combined := make([]Rule, 0, len(rs.Rules)+len(rules))
	combined = append(combined, rs.Rules...)
	combined = append(combined, rules...)
	return RuleSet{Name: rs.Name, Rules: combined}
}

// Complexity-scoring thresholds and rule weights. Naming every threshold keeps the
// classifier free of magic numbers; all are overridable via ClassifierConfig.
const (
	// DefaultMediumThreshold is the score at or above which a prompt is at least
	// Medium complexity.
	DefaultMediumThreshold = 1.5
	// DefaultComplexThreshold is the score at or above which a prompt is Complex.
	DefaultComplexThreshold = 3.5

	// Token thresholds used by the default rules.
	mediumTokenThreshold = 400
	largeTokenThreshold  = 1500
	longPromptChars      = 800
)

// DefaultRuleSet returns the built-in complexity rules. Each rule is small and
// independently testable; adding a signal is adding a rule here (or in a custom
// set passed via ClassifierConfig).
func DefaultRuleSet() RuleSet {
	return RuleSet{
		Name: "default",
		Rules: []Rule{
			{
				Name: "sizable_prompt", Description: "prompt is moderately long", Weight: 1.0,
				Features: []string{"prompt_length", "input_tokens"},
				Match: func(s Signals) bool {
					return s.PromptLength >= longPromptChars || s.InputTokens >= mediumTokenThreshold
				},
			},
			{
				Name: "large_context", Description: "input context is large", Weight: 1.5,
				Features: []string{"input_tokens", "long_context"},
				Match:    func(s Signals) bool { return s.InputTokens >= largeTokenThreshold || s.LongContext },
			},
			{
				Name: "contains_code", Description: "prompt contains source code", Weight: 1.5,
				Features: []string{"has_code"},
				Match:    func(s Signals) bool { return s.HasCode },
			},
			{
				Name: "contains_math", Description: "prompt contains mathematical content", Weight: 1.5,
				Features: []string{"has_math"},
				Match:    func(s Signals) bool { return s.HasMath },
			},
			{
				Name: "structured_data", Description: "prompt contains structured data", Weight: 0.5,
				Features: []string{"has_structured_data"},
				Match:    func(s Signals) bool { return s.HasStructuredData },
			},
			{
				Name: "multiple_instructions", Description: "prompt has several instructions", Weight: 1.0,
				Features: []string{"instruction_count"},
				Match:    func(s Signals) bool { return s.InstructionCount >= 3 },
			},
			{
				Name: "many_instructions", Description: "prompt has many instructions", Weight: 1.0,
				Features: []string{"instruction_count"},
				Match:    func(s Signals) bool { return s.InstructionCount >= 6 },
			},
			{
				Name: "reasoning_requested", Description: "prompt requests reasoning", Weight: 1.0,
				Features: []string{"reasoning_indicators"},
				Match:    func(s Signals) bool { return s.ReasoningIndicators >= 1 },
			},
			{
				Name: "multi_step_reasoning", Description: "prompt requests multi-step reasoning", Weight: 1.0,
				Features: []string{"reasoning_indicators"},
				Match:    func(s Signals) bool { return s.ReasoningIndicators >= 3 },
			},
			{
				Name: "long_conversation", Description: "long conversation history", Weight: 0.5,
				Features: []string{"conversation_history"},
				Match:    func(s Signals) bool { return s.ConversationHistory >= 6 },
			},
		},
	}
}
