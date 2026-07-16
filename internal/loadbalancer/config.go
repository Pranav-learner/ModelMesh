package loadbalancer

import "fmt"

// Strategy name constants. Only StrategyRoundRobin and StrategyLeastLatency are
// implemented; the remainder are reserved extension points that Build reports as
// not-implemented, so configuration can already name them.
const (
	StrategyRoundRobin   = "round_robin"
	StrategyLeastLatency = "least_latency"

	StrategyWeightedRoundRobin = "weighted_round_robin" // reserved
	StrategyLeastConnections   = "least_connections"    // reserved
	StrategyRandom             = "random"               // reserved
	StrategyConsistentHashing  = "consistent_hashing"   // reserved
)

var reservedStrategies = map[string]struct{}{
	StrategyWeightedRoundRobin: {},
	StrategyLeastConnections:   {},
	StrategyRandom:             {},
	StrategyConsistentHashing:  {},
}

func isReserved(name string) bool {
	_, ok := reservedStrategies[name]
	return ok
}

// DefaultLatencyWindow is the number of recent requests each instance's rolling
// latency average is computed over.
const DefaultLatencyWindow = 20

// Config configures a load balancer. It is intentionally small; per-instance
// tuning lives on the Instance, and algorithm specifics live in the Strategy.
type Config struct {
	// Strategy is the selection algorithm name (see the Strategy* constants).
	Strategy string `json:"strategy"`
	// LatencyWindow is the rolling window size (in requests) used for each
	// instance's latency average.
	LatencyWindow int `json:"latency_window"`
}

// DefaultConfig returns the default configuration: round-robin selection over a
// 20-request latency window.
func DefaultConfig() Config {
	return Config{Strategy: StrategyRoundRobin, LatencyWindow: DefaultLatencyWindow}
}

// WithDefaults returns a copy of c with zero-valued fields replaced by defaults.
func (c Config) WithDefaults() Config {
	if c.Strategy == "" {
		c.Strategy = StrategyRoundRobin
	}
	if c.LatencyWindow <= 0 {
		c.LatencyWindow = DefaultLatencyWindow
	}
	return c
}

// Validate reports whether the configuration is structurally valid.
func (c Config) Validate() error {
	if c.LatencyWindow <= 0 {
		return fmt.Errorf("%w: latency_window must be positive, got %d", ErrInvalidConfig, c.LatencyWindow)
	}
	if c.Strategy == "" {
		return fmt.Errorf("%w: strategy must not be empty", ErrInvalidConfig)
	}
	return nil
}
