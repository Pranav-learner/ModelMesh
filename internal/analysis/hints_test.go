package analysis

import "testing"

func TestHintGenerator_TierByComplexity(t *testing.T) {
	g := NewHintGenerator(DefaultHintConfig())
	cases := []struct {
		complexity Complexity
		wantTier   ModelTier
	}{
		{ComplexitySimple, TierSmall},
		{ComplexityMedium, TierStandard},
		{ComplexityComplex, TierLarge},
	}
	for _, tc := range cases {
		var h RoutingHints
		g.Generate(Signals{}, Classification{Complexity: tc.complexity}, &h)
		if h.PreferredModelTier != tc.wantTier {
			t.Errorf("%s → tier %s, want %s", tc.complexity, h.PreferredModelTier, tc.wantTier)
		}
		if h.Complexity != tc.complexity {
			t.Errorf("hint complexity = %s, want %s", h.Complexity, tc.complexity)
		}
	}
}

func TestHintGenerator_Sensitivities(t *testing.T) {
	g := NewHintGenerator(DefaultHintConfig())

	// Simple → latency- and cost-sensitive.
	var simple RoutingHints
	g.Generate(Signals{}, Classification{Complexity: ComplexitySimple}, &simple)
	if !simple.LatencySensitive || !simple.CostSensitive {
		t.Errorf("simple should be latency+cost sensitive: %+v", simple)
	}
	if simple.ReasoningIntensive {
		t.Errorf("simple should not be reasoning-intensive")
	}

	// Medium → cost-sensitive only.
	var medium RoutingHints
	g.Generate(Signals{}, Classification{Complexity: ComplexityMedium}, &medium)
	if medium.LatencySensitive || !medium.CostSensitive {
		t.Errorf("medium should be cost-sensitive only: %+v", medium)
	}

	// Complex → reasoning-intensive, not cost/latency sensitive.
	var complex RoutingHints
	g.Generate(Signals{}, Classification{Complexity: ComplexityComplex}, &complex)
	if !complex.ReasoningIntensive || complex.LatencySensitive || complex.CostSensitive {
		t.Errorf("complex hints wrong: %+v", complex)
	}
}

func TestHintGenerator_HighContextAndMathReasoning(t *testing.T) {
	g := NewHintGenerator(DefaultHintConfig())

	var h RoutingHints
	reasons := g.Generate(Signals{LongContext: true, HasMath: true}, Classification{Complexity: ComplexitySimple}, &h)
	if !h.HighContext {
		t.Errorf("LongContext signal should set HighContext")
	}
	if !h.ReasoningIntensive {
		t.Errorf("math signal should set ReasoningIntensive even for simple complexity")
	}
	if len(reasons) == 0 {
		t.Errorf("expected hint reasons")
	}
}

func TestHintGenerator_ReasoningProvider(t *testing.T) {
	cfg := DefaultHintConfig()
	cfg.ReasoningProvider = "anthropic"
	g := NewHintGenerator(cfg)

	var h RoutingHints
	g.Generate(Signals{ReasoningIndicators: 3}, Classification{Complexity: ComplexityComplex}, &h)
	if h.PreferredProvider != "anthropic" {
		t.Errorf("reasoning-intensive should prefer configured provider, got %q", h.PreferredProvider)
	}
}

func TestHintGenerator_CustomTiers(t *testing.T) {
	cfg := HintConfig{TierSimple: "nano", TierMedium: "mid", TierComplex: "ultra"}
	g := NewHintGenerator(cfg)
	var h RoutingHints
	g.Generate(Signals{}, Classification{Complexity: ComplexityComplex}, &h)
	if h.PreferredModelTier != "ultra" {
		t.Errorf("custom complex tier = %s, want ultra", h.PreferredModelTier)
	}
}
