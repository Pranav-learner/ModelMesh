package routing

import (
	"errors"
	"testing"
)

func TestDefaultConfig_Valid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v", err)
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	c := Config{}.WithDefaults()
	if c.Strategy != StrategyWeighted {
		t.Errorf("Strategy = %q, want weighted", c.Strategy)
	}
	if c.Weighted.DefaultWeight != DefaultWeight {
		t.Errorf("DefaultWeight = %v, want %v", c.Weighted.DefaultWeight, DefaultWeight)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"default", DefaultConfig(), false},
		{"empty strategy", Config{Strategy: ""}, true},
		{"negative default weight", Config{Strategy: "weighted", Weighted: WeightedConfig{DefaultWeight: -1, Factors: FactorWeights{Cost: 1}}}, true},
		{"negative provider weight", Config{Strategy: "weighted", Weighted: WeightedConfig{Weights: map[string]float64{"x": -2}, Factors: FactorWeights{Cost: 1}}}, true},
		{"valid tie-break weights", Config{Strategy: "weighted", Weighted: WeightedConfig{Weights: map[string]float64{"x": 3}, Factors: FactorWeights{Cost: 1}}}, false},
		{"zero total factor weight", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{}}}, true},
		{"negative factor weight", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{Cost: -1, Quality: 1}}}, true},
		{"negative pricing", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{Cost: 1}, Cost: CostConfig{Default: ModelPricing{InputPer1K: -1}}}}, true},
		{"quality out of range", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{Quality: 1}, Quality: QualityConfig{Default: 1.5}}}, true},
		{"availability out of range", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{Availability: 1}, Availability: AvailabilityConfig{Healthy: 2}}}, true},
		{"negative latency", Config{Strategy: "weighted", Weighted: WeightedConfig{Factors: FactorWeights{Latency: 1}, Latency: LatencyConfig{Default: -1}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				if !errors.Is(err, ErrInvalidRoutingConfig) {
					t.Errorf("error does not wrap ErrInvalidRoutingConfig: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
