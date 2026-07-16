package shadow

import (
	"fmt"
	"time"
)

// Policy name constants. Disabled and FixedPercentage are implemented;
// RuleBased is a reserved extension point, nameable in config.
const (
	PolicyDisabled        = "disabled"
	PolicyFixedPercentage = "fixed_percentage"
	PolicyRuleBased       = "rule_based" // reserved
)

var reservedPolicies = map[string]struct{}{
	PolicyRuleBased: {},
}

func isReserved(name string) bool {
	_, ok := reservedPolicies[name]
	return ok
}

// Defaults.
const (
	// DefaultMaxTrackedExecutions bounds how many recent executions are retained.
	DefaultMaxTrackedExecutions = 256
	// DefaultShadowTimeout bounds a single shadow request's duration.
	DefaultShadowTimeout = 30 * time.Second
)

// Config configures the shadow framework.
type Config struct {
	// Policy is the sampling policy name (see the Policy* constants).
	Policy string `json:"policy"`
	// Percentage is the fixed sampling percentage in [0,100] for the
	// fixed-percentage policy (e.g. 1, 5, 10, 25, 50, 100).
	Percentage float64 `json:"percentage"`
	// MaxTrackedExecutions bounds the retained recent-execution history.
	MaxTrackedExecutions int `json:"max_tracked_executions"`
	// ShadowTimeout bounds a single shadow request.
	ShadowTimeout time.Duration `json:"shadow_timeout"`
}

// DefaultConfig returns a disabled shadow configuration (safe default: no
// shadow traffic until explicitly enabled).
func DefaultConfig() Config {
	return Config{
		Policy:               PolicyDisabled,
		MaxTrackedExecutions: DefaultMaxTrackedExecutions,
		ShadowTimeout:        DefaultShadowTimeout,
	}
}

// WithDefaults returns a copy of c with zero-valued fields replaced by defaults.
func (c Config) WithDefaults() Config {
	if c.Policy == "" {
		c.Policy = PolicyDisabled
	}
	if c.MaxTrackedExecutions <= 0 {
		c.MaxTrackedExecutions = DefaultMaxTrackedExecutions
	}
	if c.ShadowTimeout <= 0 {
		c.ShadowTimeout = DefaultShadowTimeout
	}
	return c
}

// Validate reports whether the configuration is structurally valid.
func (c Config) Validate() error {
	if c.Policy == "" {
		return fmt.Errorf("%w: policy must not be empty", ErrInvalidConfig)
	}
	if c.Percentage < 0 || c.Percentage > 100 {
		return fmt.Errorf("%w: percentage must be in [0,100], got %g", ErrInvalidConfig, c.Percentage)
	}
	return nil
}
