package routing

import "fmt"

// Strategy name constants. Only StrategyWeighted is implemented in this phase;
// the remainder are reserved extension points that Build reports as
// not-implemented rather than unknown, so their intent is documented in code.
const (
	StrategyWeighted     = "weighted"
	StrategyRoundRobin   = "round_robin"   // reserved
	StrategyRandom       = "random"        // reserved
	StrategyCostFirst    = "cost_first"    // reserved
	StrategyLatencyFirst = "latency_first" // reserved
)

// reservedStrategies is the set of recognized-but-unimplemented strategy names.
var reservedStrategies = map[string]struct{}{
	StrategyRoundRobin:   {},
	StrategyRandom:       {},
	StrategyCostFirst:    {},
	StrategyLatencyFirst: {},
}

func isReserved(name string) bool {
	_, ok := reservedStrategies[name]
	return ok
}

// DefaultWeight is applied to any provider without an explicit weight.
const DefaultWeight = 1.0

// WeightedConfig configures the weighted strategy. Weights are keyed by provider
// name; a later part may extend this to per-model weighting. It is defined now so
// the weighted skeleton has a stable configuration shape to grow into.
type WeightedConfig struct {
	// Weights maps provider name to its routing weight.
	Weights map[string]float64
	// DefaultWeight is used for providers absent from Weights.
	DefaultWeight float64
}

// Config configures the routing framework. Per-strategy configuration lives in
// typed sub-structs (Weighted, ...) so the config grows additively as strategies
// are added, without an untyped options bag.
type Config struct {
	// Strategy is the active strategy name. Defaults to StrategyWeighted.
	Strategy string
	// AllowFallback is the default fallback intent when a RoutingContext does not
	// set its own.
	AllowFallback bool
	// Weighted holds the weighted strategy configuration.
	Weighted WeightedConfig
}

// DefaultConfig returns a valid default routing configuration.
func DefaultConfig() Config {
	return Config{
		Strategy:      StrategyWeighted,
		AllowFallback: true,
		Weighted:      WeightedConfig{DefaultWeight: DefaultWeight},
	}
}

// WithDefaults returns a copy of c with zero-valued fields filled in.
func (c Config) WithDefaults() Config {
	if c.Strategy == "" {
		c.Strategy = StrategyWeighted
	}
	// Fill only a genuinely-unset (zero) weight. A negative value is invalid, not
	// unset, and must be surfaced by Validate rather than silently corrected here.
	if c.Weighted.DefaultWeight == 0 {
		c.Weighted.DefaultWeight = DefaultWeight
	}
	return c
}

// Validate checks the routing configuration for structural validity. Errors wrap
// ErrInvalidRoutingConfig.
func (c Config) Validate() error {
	if c.Strategy == "" {
		return fmt.Errorf("%w: strategy must not be empty", ErrInvalidRoutingConfig)
	}
	if c.Weighted.DefaultWeight < 0 {
		return fmt.Errorf("%w: weighted.default_weight must not be negative", ErrInvalidRoutingConfig)
	}
	for name, w := range c.Weighted.Weights {
		if w < 0 {
			return fmt.Errorf("%w: weighted.weights[%q] must not be negative", ErrInvalidRoutingConfig, name)
		}
	}
	return nil
}
