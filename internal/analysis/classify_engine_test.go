package analysis_test

import (
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func userReq(content string) provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: content}}}
}

func TestEngine_ClassifiesAndHints(t *testing.T) {
	e := analysis.New()

	// Simple request.
	simple := e.Analyze(context.Background(), userReq("What is the capital of France?"))
	if simple.Classification.Complexity != analysis.ComplexitySimple {
		t.Errorf("simple = %s, want simple", simple.Classification.Complexity)
	}
	if simple.Hints.PreferredModelTier != analysis.TierSmall {
		t.Errorf("simple tier = %s, want small", simple.Hints.PreferredModelTier)
	}
	if !simple.Hints.LatencySensitive || !simple.Hints.CostSensitive {
		t.Errorf("simple should be latency/cost sensitive")
	}

	// Complex request: code + math + multi-step reasoning.
	complex := e.Analyze(context.Background(), userReq(
		"```python\ndef solve(): pass\n```\nExplain step by step and prove the complexity is O(n^2). Compare with mergesort."))
	if complex.Classification.Complexity != analysis.ComplexityComplex {
		t.Errorf("complex = %s (score %.1f), want complex", complex.Classification.Complexity, complex.Classification.Score)
	}
	if complex.Hints.PreferredModelTier != analysis.TierLarge {
		t.Errorf("complex tier = %s, want large", complex.Hints.PreferredModelTier)
	}
	if !complex.Hints.ReasoningIntensive {
		t.Errorf("complex code+math+reasoning should be reasoning-intensive")
	}
}

func TestEngine_DeliverableFieldsPresent(t *testing.T) {
	// Every request must now carry complexity, routing hints, and an explanation.
	e := analysis.New()
	res := e.Analyze(context.Background(), userReq("Write and explain a bubble sort in Go."))

	if res.Classification.Complexity == "" {
		t.Errorf("missing complexity")
	}
	if res.Hints.PreferredModelTier == "" {
		t.Errorf("missing routing hint tier")
	}
	if res.Classification.Confidence <= 0 {
		t.Errorf("missing confidence")
	}
	if len(res.Classification.HintReasons) == 0 {
		t.Errorf("missing hint reasons")
	}
}

func TestEngine_ExplainRendersAllSections(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), userReq(
		"```go\nfunc f() {}\n```\nExplain why this works step by step and compare approaches."))
	explanation := res.Explain()

	for _, want := range []string{"Complexity:", "confidence", "Features used:", "Rules triggered:", "Generated hints:", "contains_code"} {
		if !strings.Contains(explanation, want) {
			t.Errorf("Explain() missing %q:\n%s", want, explanation)
		}
	}
}

func TestEngine_AttributesCarryComplexityHints(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), userReq("Explain step by step and prove it. ```go\nx:=1\n```"))
	attrs := res.Attributes()

	if attrs[analysis.AttrComplexity] != string(res.Hints.Complexity) {
		t.Errorf("complexity attr = %v, want %s", attrs[analysis.AttrComplexity], res.Hints.Complexity)
	}
	if _, ok := attrs[analysis.AttrPreferredModelTier].(string); !ok {
		t.Errorf("preferred_model_tier attr missing")
	}
	if attrs[analysis.AttrReasoningIntensive] != true {
		t.Errorf("reasoning_intensive attr should be true")
	}
	for _, k := range []string{analysis.AttrLatencySensitive, analysis.AttrCostSensitive, analysis.AttrHighContext} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("attribute %q missing", k)
		}
	}
}

func TestEngine_ConfigurableViaOptions(t *testing.T) {
	// Custom thresholds + tiers flow through the engine.
	e := analysis.New(
		analysis.WithClassifierConfig(analysis.ClassifierConfig{
			RuleSet: analysis.DefaultRuleSet(), MediumThreshold: 0.4, ComplexThreshold: 0.9,
		}),
		analysis.WithHintConfig(analysis.HintConfig{TierSimple: "nano", TierMedium: "mid", TierComplex: "ultra"}),
	)
	// Structured data alone (0.5) now reaches Medium under the low thresholds.
	res := e.Analyze(context.Background(), userReq(`{"key": "value", "n": 1}`))
	if res.Classification.Complexity != analysis.ComplexityMedium {
		t.Errorf("with low thresholds, JSON = %s, want medium", res.Classification.Complexity)
	}
	if res.Hints.PreferredModelTier != "mid" {
		t.Errorf("custom medium tier = %s, want mid", res.Hints.PreferredModelTier)
	}
}
