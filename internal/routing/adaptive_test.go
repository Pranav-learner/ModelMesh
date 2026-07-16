package routing

import (
	"context"
	"testing"
)

// TestWeighted_FactorWeightsOverride proves adaptive, request-aware weighting: one
// statically-configured strategy flips its winner based on a per-request
// factor-weight override carried on the routing context — no reconfiguration.
func TestWeighted_FactorWeightsOverride(t *testing.T) {
	// Cheaper model has lower quality; the static config is quality-leaning.
	cfg := WeightedConfig{
		Factors: FactorWeights{Cost: 0.25, Latency: 0.25, Availability: 0.25, Quality: 0.25},
		Cost: CostConfig{Pricing: map[string]ModelPricing{
			"cheap": {InputPer1K: 0.001}, "premium": {InputPer1K: 0.05},
		}, EstimatedInputTokens: 1000},
		Quality: QualityConfig{Models: map[string]float64{"cheap": 0.60, "premium": 0.99}},
	}
	s := NewWeighted(cfg)
	in := []Candidate{cand("a", "cheap"), cand("b", "premium")}

	// Cost-dominant override → cheap wins (simulates a "simple" prompt).
	costCtx := RoutingContext{Attributes: map[string]any{
		AttrFactorWeights: FactorWeights{Cost: 0.9, Latency: 0.03, Availability: 0.03, Quality: 0.04}.ToMap(),
	}}
	if out := rankProviders(t, s, costCtx, in); out[0].Model != "cheap" {
		t.Errorf("cost-dominant override winner = %q, want cheap", out[0].Model)
	}

	// Quality-dominant override → premium wins (simulates a "complex" prompt),
	// from the SAME strategy instance.
	qualCtx := RoutingContext{Attributes: map[string]any{
		AttrFactorWeights: FactorWeights{Cost: 0.04, Latency: 0.03, Availability: 0.03, Quality: 0.9}.ToMap(),
	}}
	if out := rankProviders(t, s, qualCtx, in); out[0].Model != "premium" {
		t.Errorf("quality-dominant override winner = %q, want premium", out[0].Model)
	}

	// No override → the balanced static config applies (baseline unchanged).
	if _, err := s.Rank(context.Background(), RoutingContext{}, in); err != nil {
		t.Fatalf("baseline rank error: %v", err)
	}
}

func TestFactorWeightsOverride_IgnoresInvalid(t *testing.T) {
	// A zero/empty override is ignored (falls back to static weights).
	if _, ok := factorWeightsOverride(RoutingContext{}); ok {
		t.Errorf("nil attributes should yield no override")
	}
	rc := RoutingContext{Attributes: map[string]any{AttrFactorWeights: map[string]float64{"cost": 0, "quality": 0}}}
	if _, ok := factorWeightsOverride(rc); ok {
		t.Errorf("all-zero override should be ignored")
	}
	rc2 := RoutingContext{Attributes: map[string]any{AttrFactorWeights: "not a map"}}
	if _, ok := factorWeightsOverride(rc2); ok {
		t.Errorf("wrong-typed override should be ignored")
	}
}

func TestFactorWeights_ToMap(t *testing.T) {
	m := FactorWeights{Cost: 1, Latency: 2, Availability: 3, Quality: 4}.ToMap()
	if m[ScorerCost] != 1 || m[ScorerLatency] != 2 || m[ScorerAvailability] != 3 || m[ScorerQuality] != 4 {
		t.Errorf("ToMap = %v", m)
	}
}
