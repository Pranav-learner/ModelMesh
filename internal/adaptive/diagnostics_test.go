package adaptive_test

import (
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func TestExplainAdaptiveWeighting(t *testing.T) {
	w := adaptive.New(adaptive.DefaultConfig())
	r := w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityComplex, ReasoningIntensive: true})

	out := adaptive.ExplainAdaptiveWeighting(r)
	for _, want := range []string{"complex", "base", "adjusted", "quality"} {
		if !strings.Contains(out, want) {
			t.Errorf("ExplainAdaptiveWeighting missing %q:\n%s", want, out)
		}
	}

	// No-change path.
	none := adaptive.ExplainAdaptiveWeighting(w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityMedium}))
	if !strings.Contains(none, "no adjustments") {
		t.Errorf("expected no-adjustments note:\n%s", none)
	}
}

func TestExplainRoutingHints(t *testing.T) {
	out := adaptive.ExplainRoutingHints(analysis.RoutingHints{
		Complexity: analysis.ComplexitySimple, PreferredModelTier: analysis.TierSmall,
		LatencySensitive: true, CostSensitive: true,
	})
	for _, want := range []string{"simple", "small", "latency-sensitive", "cost-sensitive"} {
		if !strings.Contains(out, want) {
			t.Errorf("ExplainRoutingHints missing %q: %s", want, out)
		}
	}
}

func TestShowRoutingDecision(t *testing.T) {
	d := routing.RoutingDecision{
		Strategy: "weighted",
		Selected: routing.Candidate{Provider: "openai", Model: "gpt-4o-mini", Score: 0.82, Reason: "won on cost"},
		Candidates: []routing.Candidate{
			{Provider: "openai", Model: "gpt-4o-mini", Score: 0.82},
			{Provider: "anthropic", Model: "claude-sonnet", Score: 0.71},
		},
		Explanation: routing.RoutingExplanation{Weights: map[string]float64{"cost": 0.6, "quality": 0.4}},
	}
	out := adaptive.ShowRoutingDecision(d)
	for _, want := range []string{"openai/gpt-4o-mini", "0.820", "won on cost", "candidates", "claude-sonnet"} {
		if !strings.Contains(out, want) {
			t.Errorf("ShowRoutingDecision missing %q:\n%s", want, out)
		}
	}
}
