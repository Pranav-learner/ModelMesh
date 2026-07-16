package routing

import (
	"context"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// shared test helpers ---------------------------------------------------------

type stubHealth map[string]provider.HealthStatus

func (s stubHealth) Health(name string) (provider.HealthStatus, bool) {
	h, ok := s[name]
	return h, ok
}

func healthyStatus() provider.HealthStatus {
	return provider.HealthStatus{State: provider.HealthStateHealthy}
}
func degradedStatus() provider.HealthStatus {
	return provider.HealthStatus{State: provider.HealthStateDegraded}
}
func unhealthyStatus() provider.HealthStatus {
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}
}

func scores(t *testing.T, s Scorer, rc RoutingContext, cs []Candidate) []float64 {
	t.Helper()
	out, err := s.Scores(context.Background(), rc, cs)
	if err != nil {
		t.Fatalf("%s.Scores() = %v", s.Name(), err)
	}
	return out
}

// normalization ---------------------------------------------------------------

func TestNormalizeLowerIsBetter(t *testing.T) {
	got := normalizeLowerIsBetter([]float64{1, 2, 3})
	if got[0] != 1.0 || got[2] != 0.0 {
		t.Errorf("min-max: cheapest should be 1.0, dearest 0.0, got %v", got)
	}
	if got[1] <= 0 || got[1] >= 1 {
		t.Errorf("middle value should be strictly between 0 and 1, got %v", got[1])
	}
	// Degenerate cases yield 1.0 for every element.
	if all := normalizeLowerIsBetter([]float64{5, 5, 5}); all[0] != 1.0 || all[1] != 1.0 {
		t.Errorf("all-equal should be 1.0, got %v", all)
	}
	if one := normalizeLowerIsBetter([]float64{7}); one[0] != 1.0 {
		t.Errorf("single value should be 1.0, got %v", one)
	}
	if len(normalizeLowerIsBetter(nil)) != 0 {
		t.Errorf("empty input should return empty")
	}
}

func TestNormalizeWeights(t *testing.T) {
	w := normalizeWeights(map[string]float64{"a": 1, "b": 3})
	if w["a"] != 0.25 || w["b"] != 0.75 {
		t.Errorf("normalized weights = %v, want a:0.25 b:0.75", w)
	}
	// Zero total falls back to equal weighting.
	eq := normalizeWeights(map[string]float64{"a": 0, "b": 0})
	if eq["a"] != 0.5 || eq["b"] != 0.5 {
		t.Errorf("zero-total fallback = %v, want equal 0.5", eq)
	}
}

// cost scorer -----------------------------------------------------------------

func TestCostScorer(t *testing.T) {
	s := NewCostScorer(CostConfig{
		Pricing: map[string]ModelPricing{
			"cheap": {InputPer1K: 0.001, OutputPer1K: 0.002},
			"dear":  {InputPer1K: 0.02, OutputPer1K: 0.06},
		},
		EstimatedInputTokens:  1000,
		EstimatedOutputTokens: 1000,
	})

	got := scores(t, s, RoutingContext{}, []Candidate{cand("a", "cheap"), cand("b", "dear")})
	if got[0] != 1.0 || got[1] != 0.0 {
		t.Errorf("cost scores = %v, want cheap=1.0 dear=0.0", got)
	}

	// Unknown model falls back to default pricing (zero here) => cost 0 for all.
	sd := NewCostScorer(CostConfig{})
	if g := scores(t, sd, RoutingContext{}, []Candidate{cand("a", "x"), cand("b", "y")}); g[0] != 1.0 || g[1] != 1.0 {
		t.Errorf("default (free) pricing should tie at 1.0, got %v", g)
	}
}

func TestCostScorer_EstimateCost(t *testing.T) {
	s := NewCostScorer(CostConfig{
		Default:              ModelPricing{InputPer1K: 0.01, OutputPer1K: 0.03},
		EstimatedInputTokens: 2000, EstimatedOutputTokens: 1000,
	})
	// 2000/1000*0.01 + 1000/1000*0.03 = 0.02 + 0.03 = 0.05
	if got := s.EstimateCost(RoutingContext{}, cand("p", "m")); got != 0.05 {
		t.Errorf("EstimateCost = %v, want 0.05", got)
	}
}

// latency scorer --------------------------------------------------------------

func TestLatencyScorer(t *testing.T) {
	s := NewLatencyScorer(LatencyConfig{
		Providers: map[string]time.Duration{"fast": 100 * time.Millisecond, "slow": 900 * time.Millisecond},
		Models:    map[string]time.Duration{"turbo": 50 * time.Millisecond},
	})
	got := scores(t, s, RoutingContext{}, []Candidate{cand("fast", "m"), cand("slow", "m")})
	if got[0] != 1.0 || got[1] != 0.0 {
		t.Errorf("latency scores = %v, want fast=1.0 slow=0.0", got)
	}
	// Model override beats provider value.
	if d := s.ExpectedLatency(cand("slow", "turbo")); d != 50*time.Millisecond {
		t.Errorf("model latency override = %v, want 50ms", d)
	}
}

// availability scorer ---------------------------------------------------------

func TestAvailabilityScorer_HealthStates(t *testing.T) {
	health := stubHealth{"h": healthyStatus(), "d": degradedStatus(), "u": unhealthyStatus()}
	s := NewAvailabilityScorer(AvailabilityConfig{}, health)

	got := scores(t, s, RoutingContext{}, []Candidate{cand("h", "m"), cand("d", "m"), cand("u", "m"), cand("unknownp", "m")})
	want := []float64{DefaultAvailabilityHealthy, DefaultAvailabilityDegraded, DefaultAvailabilityUnhealthy, DefaultAvailabilityUnknown}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("availability[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAvailabilityScorer_NoHealthUsesUnknown(t *testing.T) {
	s := NewAvailabilityScorer(AvailabilityConfig{}, nil) // nil -> NoHealth
	got := scores(t, s, RoutingContext{}, []Candidate{cand("anything", "m")})
	if got[0] != DefaultAvailabilityUnknown {
		t.Errorf("no-health score = %v, want unknown default %v", got[0], DefaultAvailabilityUnknown)
	}
}

func TestAvailabilityScorer_OverrideWins(t *testing.T) {
	s := NewAvailabilityScorer(AvailabilityConfig{Overrides: map[string]float64{"p": 0.33}}, stubHealth{"p": unhealthyStatus()})
	if got := scores(t, s, RoutingContext{}, []Candidate{cand("p", "m")}); got[0] != 0.33 {
		t.Errorf("override score = %v, want 0.33", got[0])
	}
}

// quality scorer --------------------------------------------------------------

func TestQualityScorer(t *testing.T) {
	s := NewQualityScorer(QualityConfig{
		Models:    map[string]float64{"gpt-4.1": 0.98, "claude-haiku": 0.80},
		Providers: map[string]float64{"openai": 0.90},
		Default:   0.5,
	})
	got := scores(t, s, RoutingContext{}, []Candidate{
		cand("openai", "gpt-4.1"),      // model value
		cand("openai", "unknown"),      // provider value
		cand("other", "unknown-model"), // default
	})
	if got[0] != 0.98 || got[1] != 0.90 || got[2] != 0.5 {
		t.Errorf("quality precedence = %v, want [0.98 0.90 0.5]", got)
	}
}

// aggregation -----------------------------------------------------------------

func TestAggregate(t *testing.T) {
	scorers := []Scorer{
		NewCostScorer(CostConfig{Pricing: map[string]ModelPricing{"a": {InputPer1K: 0.001}, "b": {InputPer1K: 0.01}}, EstimatedInputTokens: 1000}),
		NewQualityScorer(QualityConfig{Models: map[string]float64{"a": 0.5, "b": 1.0}}),
	}
	weights := normalizeWeights(map[string]float64{ScorerCost: 1, ScorerQuality: 1})

	bds, err := aggregate(context.Background(), RoutingContext{}, []Candidate{cand("pa", "a"), cand("pb", "b")}, scorers, weights)
	if err != nil {
		t.Fatalf("aggregate() = %v", err)
	}
	// a: cost 1.0 (cheaper), quality 0.5 -> 0.5*1.0 + 0.5*0.5 = 0.75
	// b: cost 0.0 (dearer), quality 1.0 -> 0.5*0.0 + 0.5*1.0 = 0.50
	if !almostEqual(bds[0].Final, 0.75) || !almostEqual(bds[1].Final, 0.50) {
		t.Errorf("finals = %v / %v, want 0.75 / 0.50", bds[0].Final, bds[1].Final)
	}
}

func TestAggregate_ScorerCountMismatch(t *testing.T) {
	bad := brokenScorer{}
	_, err := aggregate(context.Background(), RoutingContext{}, []Candidate{cand("a", "m")}, []Scorer{bad}, map[string]float64{"broken": 1})
	if err == nil {
		t.Fatalf("aggregate() = nil, want error on score-count mismatch")
	}
}

type brokenScorer struct{}

func (brokenScorer) Name() string { return "broken" }
func (brokenScorer) Scores(context.Context, RoutingContext, []Candidate) ([]float64, error) {
	return []float64{}, nil // wrong length
}
