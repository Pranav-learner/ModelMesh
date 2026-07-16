package adaptive_test

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func TestWeigher_SimpleFavorsCost(t *testing.T) {
	w := adaptive.New(adaptive.DefaultConfig())
	r := w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexitySimple, LatencySensitive: true, CostSensitive: true})

	if r.Adjusted.Cost <= r.Base.Cost {
		t.Errorf("simple should raise cost weight: base %.2f → %.2f", r.Base.Cost, r.Adjusted.Cost)
	}
	if r.Adjusted.Quality >= r.Base.Quality {
		t.Errorf("simple should lower quality weight: base %.2f → %.2f", r.Base.Quality, r.Adjusted.Quality)
	}
	if r.Adjusted.Latency <= r.Base.Latency {
		t.Errorf("latency-sensitive should raise latency weight")
	}
	if !r.Changed() || r.Complexity != analysis.ComplexitySimple {
		t.Errorf("expected a changed result for simple")
	}
}

func TestWeigher_ComplexFavorsQuality(t *testing.T) {
	w := adaptive.New(adaptive.DefaultConfig())
	r := w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityComplex, ReasoningIntensive: true})

	if r.Adjusted.Quality <= r.Base.Quality {
		t.Errorf("complex should raise quality weight: base %.2f → %.2f", r.Base.Quality, r.Adjusted.Quality)
	}
	if r.Adjusted.Cost >= r.Base.Cost {
		t.Errorf("complex should lower cost weight: base %.2f → %.2f", r.Base.Cost, r.Adjusted.Cost)
	}
}

func TestWeigher_MediumNoFlagsUnchanged(t *testing.T) {
	w := adaptive.New(adaptive.DefaultConfig())
	r := w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityMedium})
	if r.Changed() {
		t.Errorf("medium with no hints should not adjust weights: %+v", r.Adjustments)
	}
	if r.Adjusted != r.Base {
		t.Errorf("adjusted should equal base: %+v vs %+v", r.Adjusted, r.Base)
	}
}

func TestWeigher_ClampsAtMinWeight(t *testing.T) {
	cfg := adaptive.DefaultConfig()
	cfg.Base = routing.FactorWeights{Cost: 0.1, Latency: 0.1, Availability: 0.1, Quality: 0.1}
	cfg.SimpleQualityPenalty = 1.0 // would drive quality far negative
	cfg.MinWeight = 0.05
	w := adaptive.New(cfg)

	r := w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexitySimple})
	if r.Adjusted.Quality < cfg.MinWeight {
		t.Errorf("quality weight %.3f fell below floor %.3f", r.Adjusted.Quality, cfg.MinWeight)
	}
}

func TestWeigher_RecordsMetrics(t *testing.T) {
	col := adaptive.NewCollector()
	w := adaptive.New(adaptive.DefaultConfig(), adaptive.WithMetrics(col))

	w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexitySimple, CostSensitive: true})
	w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityComplex, ReasoningIntensive: true})
	w.Adapt(analysis.RoutingHints{Complexity: analysis.ComplexityComplex})

	s := col.Snapshot()
	if s.Total != 3 {
		t.Errorf("total = %d, want 3", s.Total)
	}
	if s.Distribution["complex"] != 2 || s.Distribution["simple"] != 1 {
		t.Errorf("distribution = %v", s.Distribution)
	}
	// avg = (1 + 3 + 3)/3 = 2.33
	if s.AverageComplexity < 2.3 || s.AverageComplexity > 2.4 {
		t.Errorf("average complexity = %.2f, want ~2.33", s.AverageComplexity)
	}
	if s.HintUsage[analysis.AttrCostSensitive] != 1 || s.HintUsage[analysis.AttrReasoningIntensive] != 1 {
		t.Errorf("hint usage = %v", s.HintUsage)
	}
	if s.WeightChanges == 0 {
		t.Errorf("expected recorded weight changes")
	}
}

func TestWeigher_RoutingAccuracy(t *testing.T) {
	cfg := adaptive.DefaultConfig()
	cfg.ModelTiers = map[string]analysis.ModelTier{
		"gpt-4o-mini":   analysis.TierSmall,
		"claude-sonnet": analysis.TierLarge,
	}
	col := adaptive.NewCollector()
	w := adaptive.New(cfg, adaptive.WithMetrics(col))

	w.RecordOutcome(analysis.TierSmall, "gpt-4o-mini")   // match
	w.RecordOutcome(analysis.TierLarge, "claude-sonnet") // match
	w.RecordOutcome(analysis.TierSmall, "claude-sonnet") // miss
	w.RecordOutcome(analysis.TierSmall, "unknown-model") // unknown → not counted

	s := col.Snapshot()
	if s.AccuracySamples != 3 {
		t.Errorf("accuracy samples = %d, want 3", s.AccuracySamples)
	}
	if s.Accuracy < 0.66 || s.Accuracy > 0.67 {
		t.Errorf("accuracy = %.2f, want ~0.67 (2/3)", s.Accuracy)
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := adaptive.DefaultConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
	if err := (adaptive.Config{Base: routing.FactorWeights{}}).Validate(); err == nil {
		t.Errorf("zero base weights should be invalid")
	}
	if err := (adaptive.Config{Base: routing.FactorWeights{Cost: 1}, MinWeight: -1}).Validate(); err == nil {
		t.Errorf("negative min weight should be invalid")
	}
}
