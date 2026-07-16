package routing

import (
	"context"
	"testing"
)

func TestWeighted_Name(t *testing.T) {
	if NewWeighted(WeightedConfig{}).Name() != StrategyWeighted {
		t.Errorf("Name() != weighted")
	}
}

func TestWeighted_AttachesWeights(t *testing.T) {
	s := NewWeighted(WeightedConfig{
		Weights:       map[string]float64{"openai": 3, "anthropic": 1},
		DefaultWeight: 2,
	})
	in := []Candidate{
		{Provider: "openai", Model: "gpt"},
		{Provider: "anthropic", Model: "claude"},
		{Provider: "unlisted", Model: "x"},
	}

	out, err := s.Rank(context.Background(), RoutingContext{}, in)
	if err != nil {
		t.Fatalf("Rank() = %v", err)
	}

	wantWeights := map[string]float64{"openai": 3, "anthropic": 1, "unlisted": 2}
	for _, c := range out {
		if c.Weight != wantWeights[c.Provider] {
			t.Errorf("weight for %q = %v, want %v", c.Provider, c.Weight, wantWeights[c.Provider])
		}
		if c.Reason == "" {
			t.Errorf("expected a reason annotation for %q", c.Provider)
		}
	}
}

// TestWeighted_DoesNotReorder asserts the Phase 2 Part 1 boundary: even though
// weights differ, the skeleton must NOT rank by them yet — order is preserved.
func TestWeighted_DoesNotReorder(t *testing.T) {
	s := NewWeighted(WeightedConfig{Weights: map[string]float64{"low": 1, "high": 100}})
	in := []Candidate{
		{Provider: "low", Model: "a"},
		{Provider: "high", Model: "b"},
	}

	out, _ := s.Rank(context.Background(), RoutingContext{}, in)
	if out[0].Provider != "low" || out[1].Provider != "high" {
		t.Errorf("order changed to %v/%v; scoring must not be applied yet", out[0].Provider, out[1].Provider)
	}
	if out[1].Score != 0 {
		t.Errorf("Score should be zero (no scoring in this phase), got %v", out[1].Score)
	}
}

func TestWeighted_DefaultWeightApplied(t *testing.T) {
	s := NewWeighted(WeightedConfig{}) // no weights, no default -> DefaultWeight
	out, _ := s.Rank(context.Background(), RoutingContext{}, []Candidate{{Provider: "x"}})
	if out[0].Weight != DefaultWeight {
		t.Errorf("weight = %v, want default %v", out[0].Weight, DefaultWeight)
	}
}

func TestWeighted_DoesNotMutateInput(t *testing.T) {
	in := []Candidate{{Provider: "x", Model: "m"}}
	_, _ = NewWeighted(WeightedConfig{}).Rank(context.Background(), RoutingContext{}, in)
	if in[0].Weight != 0 || in[0].Reason != "" {
		t.Errorf("Rank mutated the input slice: %+v", in[0])
	}
}
