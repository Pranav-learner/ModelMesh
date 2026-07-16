package analysis

import (
	"testing"
)

func TestRuleClassifier_Bands(t *testing.T) {
	c := NewRuleClassifier(DefaultClassifierConfig())

	// Simple: a short factual question — no rules fire.
	simple := c.Classify(Signals{PromptLength: 30, InputTokens: 8, InstructionCount: 1})
	if simple.Complexity != ComplexitySimple {
		t.Errorf("simple prompt = %s (score %.1f), want simple", simple.Complexity, simple.Score)
	}
	if len(simple.TriggeredRules) != 0 {
		t.Errorf("simple prompt triggered rules: %+v", simple.TriggeredRules)
	}

	// Medium: contains code (1.5) → at least Medium, below Complex.
	medium := c.Classify(Signals{PromptLength: 200, InputTokens: 100, HasCode: true, InstructionCount: 1})
	if medium.Complexity != ComplexityMedium {
		t.Errorf("code-only prompt = %s (score %.1f), want medium", medium.Complexity, medium.Score)
	}

	// Complex: code + math + reasoning + instructions → well over the threshold.
	complex := c.Classify(Signals{
		PromptLength: 2000, InputTokens: 1800, HasCode: true, HasMath: true,
		InstructionCount: 6, ReasoningIndicators: 3, LongContext: false,
	})
	if complex.Complexity != ComplexityComplex {
		t.Errorf("rich prompt = %s (score %.1f), want complex", complex.Complexity, complex.Score)
	}
	if len(complex.FeaturesUsed) == 0 {
		t.Errorf("complex classification should list features used")
	}
}

func TestRuleClassifier_ScoreAccumulation(t *testing.T) {
	c := NewRuleClassifier(DefaultClassifierConfig())
	got := c.Classify(Signals{HasCode: true, HasMath: true, InstructionCount: 1})
	// contains_code (1.5) + contains_math (1.5) = 3.0
	if got.Score != 3.0 {
		t.Errorf("score = %.1f, want 3.0", got.Score)
	}
	names := map[string]bool{}
	for _, r := range got.TriggeredRules {
		names[r.Name] = true
	}
	if !names["contains_code"] || !names["contains_math"] {
		t.Errorf("triggered rules = %+v, want code+math", got.TriggeredRules)
	}
}

func TestRuleClassifier_Confidence(t *testing.T) {
	c := NewRuleClassifier(DefaultClassifierConfig())

	// A score right at the medium boundary is borderline (confidence ~0.5).
	borderline := c.Classify(Signals{HasCode: true, InstructionCount: 1}) // score 1.5 == MediumThreshold
	if borderline.Complexity != ComplexityMedium {
		t.Fatalf("expected medium at boundary, got %s", borderline.Complexity)
	}
	if borderline.Confidence > 0.6 {
		t.Errorf("boundary confidence = %.2f, want near 0.5", borderline.Confidence)
	}

	// A clearly-simple prompt is high confidence.
	simple := c.Classify(Signals{InstructionCount: 1})
	if simple.Confidence < 0.9 {
		t.Errorf("clearly-simple confidence = %.2f, want high", simple.Confidence)
	}
}

func TestClassifierConfig_ValidateAndDefaults(t *testing.T) {
	if err := DefaultClassifierConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
	// withDefaults repairs an empty/inverted config.
	repaired := ClassifierConfig{}.withDefaults()
	if repaired.MediumThreshold <= 0 || repaired.ComplexThreshold <= repaired.MediumThreshold || len(repaired.RuleSet.Rules) == 0 {
		t.Errorf("withDefaults produced invalid config: %+v", repaired)
	}
	if err := (ClassifierConfig{RuleSet: DefaultRuleSet(), MediumThreshold: 5, ComplexThreshold: 3}).Validate(); err == nil {
		t.Errorf("inverted thresholds should be invalid")
	}
}

func TestCustomThresholds(t *testing.T) {
	// Aggressive thresholds: everything with any signal is Complex.
	cfg := ClassifierConfig{RuleSet: DefaultRuleSet(), MediumThreshold: 0.4, ComplexThreshold: 0.9}
	c := NewRuleClassifier(cfg)
	got := c.Classify(Signals{HasStructuredData: true, InstructionCount: 1}) // structured_data = 0.5
	if got.Complexity != ComplexityMedium {
		t.Errorf("with low thresholds, structured data = %s, want medium", got.Complexity)
	}
}
