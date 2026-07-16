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
		{"negative default weight", Config{Strategy: "weighted", Weighted: WeightedConfig{DefaultWeight: -1}}, true},
		{"negative weight entry", Config{Strategy: "weighted", Weighted: WeightedConfig{Weights: map[string]float64{"x": -2}}}, true},
		{"valid weights", Config{Strategy: "weighted", Weighted: WeightedConfig{Weights: map[string]float64{"x": 3}}}, false},
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
