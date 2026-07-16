package analysis_test

import (
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/analysis"
)

// fixedClassifier always returns Complex — proves WithClassifier is honored.
type fixedClassifier struct{}

func (fixedClassifier) Classify(analysis.Signals) analysis.Classification {
	return analysis.Classification{Complexity: analysis.ComplexityComplex, Confidence: 1}
}

// tierOnlyHints only sets the tier — proves WithHintGenerator is honored.
type tierOnlyHints struct{}

func (tierOnlyHints) Generate(_ analysis.Signals, c analysis.Classification, h *analysis.RoutingHints) []string {
	h.Complexity = c.Complexity
	h.PreferredModelTier = "custom-tier"
	return []string{"forced tier"}
}

func TestEngine_WithCustomClassifierAndHints(t *testing.T) {
	e := analysis.New(
		analysis.WithClassifier(fixedClassifier{}),
		analysis.WithHintGenerator(tierOnlyHints{}),
	)
	res := e.Analyze(context.Background(), userReq("hi"))
	if res.Classification.Complexity != analysis.ComplexityComplex {
		t.Errorf("custom classifier not used: %s", res.Classification.Complexity)
	}
	if res.Hints.PreferredModelTier != "custom-tier" {
		t.Errorf("custom hint generator not used: %s", res.Hints.PreferredModelTier)
	}
}

func TestEngine_PreferredProviderInAttributes(t *testing.T) {
	cfg := analysis.DefaultHintConfig()
	cfg.ReasoningProvider = "anthropic"
	e := analysis.New(analysis.WithHintConfig(cfg))

	res := e.Analyze(context.Background(), userReq("Prove step by step why this holds and compare methods."))
	attrs := res.Attributes()
	if res.Hints.ReasoningIntensive && attrs[analysis.AttrPreferredProvider] != "anthropic" {
		t.Errorf("preferred_provider attr = %v, want anthropic", attrs[analysis.AttrPreferredProvider])
	}

	// A non-reasoning request omits the provider attribute entirely.
	simple := e.Analyze(context.Background(), userReq("hi"))
	if _, ok := simple.Attributes()[analysis.AttrPreferredProvider]; ok {
		t.Errorf("simple request should not carry a preferred_provider attr")
	}
}

func TestExplain_NoRulesTriggered(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), userReq("hi"))
	explanation := res.Explain()
	if !strings.Contains(explanation, "(none)") {
		t.Errorf("simple prompt explanation should note no rules / no features:\n%s", explanation)
	}
}
