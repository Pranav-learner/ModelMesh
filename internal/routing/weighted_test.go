package routing

import (
	"context"
	"testing"
	"time"
)

// helpers ---------------------------------------------------------------------

func cand(providerName, model string) Candidate {
	return Candidate{Provider: providerName, Model: model}
}

// rankProviders is a convenience returning the ranked provider order.
func rankProviders(t *testing.T, s *WeightedStrategy, rc RoutingContext, in []Candidate) []Candidate {
	t.Helper()
	out, err := s.Rank(context.Background(), rc, in)
	if err != nil {
		t.Fatalf("Rank() = %v", err)
	}
	return out
}

// tests -----------------------------------------------------------------------

func TestWeighted_Name(t *testing.T) {
	if NewWeighted(WeightedConfig{}).Name() != StrategyWeighted {
		t.Errorf("Name() != weighted")
	}
}

func TestWeighted_RanksByCost(t *testing.T) {
	// Only cost matters; the cheaper model must win.
	cfg := WeightedConfig{
		Factors: FactorWeights{Cost: 1},
		Cost: CostConfig{
			Pricing: map[string]ModelPricing{
				"cheap":  {InputPer1K: 0.001, OutputPer1K: 0.002},
				"pricey": {InputPer1K: 0.01, OutputPer1K: 0.03},
			},
			EstimatedInputTokens:  1000,
			EstimatedOutputTokens: 1000,
		},
	}
	s := NewWeighted(cfg)

	out := rankProviders(t, s, RoutingContext{}, []Candidate{cand("b", "pricey"), cand("a", "cheap")})
	if out[0].Model != "cheap" {
		t.Errorf("winner = %q, want cheap", out[0].Model)
	}
	if out[0].Factors[ScorerCost] != 1.0 || out[1].Factors[ScorerCost] != 0.0 {
		t.Errorf("cost factors = %v / %v, want 1.0 / 0.0", out[0].Factors[ScorerCost], out[1].Factors[ScorerCost])
	}
	if out[0].Score <= out[1].Score {
		t.Errorf("winner score %v not greater than runner-up %v", out[0].Score, out[1].Score)
	}
}

func TestWeighted_RanksByQuality(t *testing.T) {
	cfg := WeightedConfig{
		Factors: FactorWeights{Quality: 1},
		Quality: QualityConfig{Models: map[string]float64{"good": 0.98, "ok": 0.80}},
	}
	s := NewWeighted(cfg)

	out := rankProviders(t, s, RoutingContext{}, []Candidate{cand("a", "ok"), cand("b", "good")})
	if out[0].Model != "good" {
		t.Errorf("winner = %q, want good", out[0].Model)
	}
}

func TestWeighted_WeightsBalanceFactors(t *testing.T) {
	// Cheaper model has worse quality; weighting decides the winner.
	cfg := func(costW, qualW float64) WeightedConfig {
		return WeightedConfig{
			Factors: FactorWeights{Cost: costW, Quality: qualW},
			Cost: CostConfig{Pricing: map[string]ModelPricing{
				"cheap": {InputPer1K: 0.001}, "premium": {InputPer1K: 0.05},
			}, EstimatedInputTokens: 1000},
			Quality: QualityConfig{Models: map[string]float64{"cheap": 0.60, "premium": 0.99}},
		}
	}
	in := []Candidate{cand("a", "cheap"), cand("b", "premium")}

	// Cost-dominant weighting -> cheap wins.
	if out := rankProviders(t, NewWeighted(cfg(0.9, 0.1)), RoutingContext{}, in); out[0].Model != "cheap" {
		t.Errorf("cost-dominant winner = %q, want cheap", out[0].Model)
	}
	// Quality-dominant weighting -> premium wins.
	if out := rankProviders(t, NewWeighted(cfg(0.1, 0.9)), RoutingContext{}, in); out[0].Model != "premium" {
		t.Errorf("quality-dominant winner = %q, want premium", out[0].Model)
	}
}

func TestWeighted_LatencyPrefersFaster(t *testing.T) {
	cfg := WeightedConfig{
		Factors: FactorWeights{Latency: 1},
		Latency: LatencyConfig{Providers: map[string]time.Duration{
			"fast": 100 * time.Millisecond, "slow": 900 * time.Millisecond,
		}},
	}
	out := rankProviders(t, NewWeighted(cfg), RoutingContext{}, []Candidate{cand("slow", "m"), cand("fast", "m")})
	if out[0].Provider != "fast" {
		t.Errorf("winner = %q, want fast", out[0].Provider)
	}
}

func TestWeighted_AvailabilityUsesHealth(t *testing.T) {
	cfg := WeightedConfig{Factors: FactorWeights{Availability: 1}}
	health := stubHealth{"healthy-provider": healthyStatus(), "sick-provider": unhealthyStatus()}
	s := NewWeighted(cfg, WithHealthProvider(health))

	out := rankProviders(t, s, RoutingContext{}, []Candidate{cand("sick-provider", "m"), cand("healthy-provider", "m")})
	if out[0].Provider != "healthy-provider" {
		t.Errorf("winner = %q, want healthy-provider", out[0].Provider)
	}
	if out[0].Factors[ScorerAvailability] != DefaultAvailabilityHealthy {
		t.Errorf("healthy availability = %v, want %v", out[0].Factors[ScorerAvailability], DefaultAvailabilityHealthy)
	}
}

func TestWeighted_TieBreak_ByExplicitOrder(t *testing.T) {
	// All factors equal -> tie -> explicit TieBreak order decides.
	cfg := WeightedConfig{Factors: FactorWeights{Cost: 1}, TieBreak: []string{"b", "a"}}
	out := rankProviders(t, NewWeighted(cfg), RoutingContext{}, []Candidate{cand("a", "m"), cand("b", "m")})
	if out[0].Provider != "b" {
		t.Errorf("tie-break winner = %q, want b (explicit priority)", out[0].Provider)
	}
}

func TestWeighted_TieBreak_ByProviderWeight(t *testing.T) {
	cfg := WeightedConfig{Factors: FactorWeights{Cost: 1}, Weights: map[string]float64{"a": 1, "b": 5}}
	out := rankProviders(t, NewWeighted(cfg), RoutingContext{}, []Candidate{cand("a", "m"), cand("b", "m")})
	if out[0].Provider != "b" {
		t.Errorf("tie-break winner = %q, want b (higher provider weight)", out[0].Provider)
	}
}

func TestWeighted_TieBreak_ByName(t *testing.T) {
	// No explicit order, equal weights -> deterministic by provider then model name.
	cfg := WeightedConfig{Factors: FactorWeights{Cost: 1}}
	out := rankProviders(t, NewWeighted(cfg), RoutingContext{}, []Candidate{cand("z", "m"), cand("a", "m")})
	if out[0].Provider != "a" {
		t.Errorf("tie-break winner = %q, want a (name asc)", out[0].Provider)
	}
}

func TestWeighted_Deterministic(t *testing.T) {
	cfg := WeightedConfig{
		Factors: FactorWeights{Cost: 0.5, Quality: 0.5},
		Cost:    CostConfig{Pricing: map[string]ModelPricing{"m1": {InputPer1K: 0.01}, "m2": {InputPer1K: 0.02}}, EstimatedInputTokens: 500},
		Quality: QualityConfig{Models: map[string]float64{"m1": 0.9, "m2": 0.95}},
	}
	s := NewWeighted(cfg)
	in := []Candidate{cand("p1", "m1"), cand("p2", "m2")}

	first := rankProviders(t, s, RoutingContext{}, in)
	for i := 0; i < 20; i++ {
		next := rankProviders(t, s, RoutingContext{}, in)
		for j := range first {
			if next[j].Provider != first[j].Provider || next[j].Score != first[j].Score {
				t.Fatalf("non-deterministic ranking at run %d position %d", i, j)
			}
		}
	}
}

func TestWeighted_FactorsAndReasonPopulated(t *testing.T) {
	cfg := WeightedConfig{Factors: FactorWeights{Cost: 1, Quality: 1}}
	out := rankProviders(t, NewWeighted(cfg), RoutingContext{}, []Candidate{cand("a", "x"), cand("b", "y")})

	for _, name := range []string{ScorerCost, ScorerLatency, ScorerAvailability, ScorerQuality} {
		if _, ok := out[0].Factors[name]; !ok {
			t.Errorf("winner missing factor %q", name)
		}
	}
	if out[0].Reason == "" {
		t.Errorf("winner reason is empty")
	}
}

func TestWeighted_SingleCandidate(t *testing.T) {
	out := rankProviders(t, NewWeighted(WeightedConfig{Factors: FactorWeights{Cost: 1}}), RoutingContext{}, []Candidate{cand("solo", "m")})
	if len(out) != 1 || out[0].Reason != "only eligible candidate" {
		t.Errorf("single-candidate handling wrong: %+v", out)
	}
}

func TestWeighted_EmptyCandidates(t *testing.T) {
	out, err := NewWeighted(WeightedConfig{}).Rank(context.Background(), RoutingContext{}, nil)
	if err != nil || out != nil {
		t.Errorf("Rank(nil) = %v, %v, want nil/nil", out, err)
	}
}

func TestWeighted_DoesNotMutateInput(t *testing.T) {
	in := []Candidate{cand("x", "m")}
	_, _ = NewWeighted(WeightedConfig{Factors: FactorWeights{Cost: 1}}).Rank(context.Background(), RoutingContext{}, in)
	if in[0].Score != 0 || in[0].Reason != "" || in[0].Factors != nil {
		t.Errorf("Rank mutated the input slice: %+v", in[0])
	}
}

func TestWeighted_NormalizedWeights(t *testing.T) {
	cfg := WeightedConfig{Factors: FactorWeights{Cost: 1, Latency: 1, Availability: 1, Quality: 1}}
	w := NewWeighted(cfg).NormalizedWeights()
	total := 0.0
	for _, v := range w {
		total += v
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("normalized weights sum = %v, want ~1.0", total)
	}
	if w[ScorerCost] != 0.25 {
		t.Errorf("cost weight = %v, want 0.25", w[ScorerCost])
	}
}

func TestWeighted_TokenEstimatesFromAttributes(t *testing.T) {
	// A larger request should cost more; with two identical-priced models the
	// attribute-driven token estimate flows into the cost estimate.
	s := NewCostScorer(CostConfig{Default: ModelPricing{InputPer1K: 0.01}})
	small := s.EstimateCost(RoutingContext{Attributes: map[string]any{AttrEstimatedInputTokens: 100}}, cand("p", "m"))
	large := s.EstimateCost(RoutingContext{Attributes: map[string]any{AttrEstimatedInputTokens: 10000}}, cand("p", "m"))
	if !(large > small) {
		t.Errorf("cost did not scale with token estimate: small=%v large=%v", small, large)
	}
}
