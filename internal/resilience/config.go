package resilience

import (
	"fmt"
	"time"
)

// Default breaker parameters, named to keep logic free of magic numbers.
const (
	// DefaultFailureThreshold is the number of consecutive failures in Closed that
	// trips the breaker to Open.
	DefaultFailureThreshold = 5
	// DefaultSuccessThreshold is the number of successful probes in Half-Open that
	// closes the breaker.
	DefaultSuccessThreshold = 2
	// DefaultOpenTimeout is how long the breaker stays Open before probing.
	DefaultOpenTimeout = 30 * time.Second
	// DefaultHalfOpenMaxRequests is the max concurrent probe requests in Half-Open.
	DefaultHalfOpenMaxRequests = 1
)

// Config configures a circuit breaker's transition thresholds and timings.
type Config struct {
	// FailureThreshold is the number of consecutive failures in Closed required to
	// open the breaker.
	FailureThreshold int `json:"failure_threshold"`
	// SuccessThreshold is the number of successful probe requests in Half-Open
	// required to close the breaker.
	SuccessThreshold int `json:"success_threshold"`
	// OpenTimeout is the cooldown after opening before the breaker becomes
	// Half-Open (lazily, on the next request).
	OpenTimeout time.Duration `json:"open_timeout"`
	// HalfOpenMaxRequests is the maximum number of concurrent probe requests
	// admitted while Half-Open. Excess requests are rejected.
	HalfOpenMaxRequests int `json:"half_open_max_requests"`

	// IsFailure optionally classifies which errors count as failures. It returns
	// true when err should trip the breaker. When nil, any non-nil error is a
	// failure (and a nil error is a success). Injecting a classifier lets callers
	// exclude caller-fault errors (e.g. validation) from tripping the breaker.
	IsFailure func(error) bool `json:"-"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() Config {
	return Config{
		FailureThreshold:    DefaultFailureThreshold,
		SuccessThreshold:    DefaultSuccessThreshold,
		OpenTimeout:         DefaultOpenTimeout,
		HalfOpenMaxRequests: DefaultHalfOpenMaxRequests,
	}
}

// WithDefaults returns a copy of c with non-positive fields replaced by defaults.
func (c Config) WithDefaults() Config {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = DefaultFailureThreshold
	}
	if c.SuccessThreshold <= 0 {
		c.SuccessThreshold = DefaultSuccessThreshold
	}
	if c.OpenTimeout <= 0 {
		c.OpenTimeout = DefaultOpenTimeout
	}
	if c.HalfOpenMaxRequests <= 0 {
		c.HalfOpenMaxRequests = DefaultHalfOpenMaxRequests
	}
	return c
}

// Validate checks the configuration. Errors wrap ErrInvalidBreakerConfig.
func (c Config) Validate() error {
	if c.FailureThreshold < 1 {
		return fmt.Errorf("%w: failure_threshold must be at least 1", ErrInvalidBreakerConfig)
	}
	if c.SuccessThreshold < 1 {
		return fmt.Errorf("%w: success_threshold must be at least 1", ErrInvalidBreakerConfig)
	}
	if c.OpenTimeout <= 0 {
		return fmt.Errorf("%w: open_timeout must be positive", ErrInvalidBreakerConfig)
	}
	if c.HalfOpenMaxRequests < 1 {
		return fmt.Errorf("%w: half_open_max_requests must be at least 1", ErrInvalidBreakerConfig)
	}
	return nil
}
