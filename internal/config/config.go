// Package config defines ModelMesh's configuration structures and their
// validation. It is intentionally free of any source-parsing concern (files,
// environment, flags): those are wiring details that belong in the bootstrap
// layer. Keeping config as plain, validated data structures makes it trivial to
// populate from any source later and keeps this package dependency-free.
package config

import (
	"errors"
	"fmt"
	"time"
)

// ErrInvalidConfig is the sentinel wrapped by all validation failures so callers
// can match with errors.Is.
var ErrInvalidConfig = errors.New("invalid config")

// Default values used by DefaultConfig and to fill zero-valued fields.
const (
	DefaultRequestTimeout      = 30 * time.Second
	DefaultRetryCount          = 2
	DefaultHealthCheckInterval = 30 * time.Second
)

// ProviderConfig describes a single provider's configuration.
//
// Credentials are never hardcoded: APIKey holds a value that the bootstrap layer
// is expected to source from the environment (or another secret store) rather
// than from a checked-in file. An empty APIKey is permitted at config level so
// that tests and the mock provider need no credentials; each adapter decides how
// to behave when a key is absent.
type ProviderConfig struct {
	// Name is the provider identifier, matching LLMProvider.Name()
	// (e.g. "openai", "anthropic").
	Name string
	// Enabled allows a configured provider to be turned off without removing it.
	Enabled bool
	// APIKey is the provider credential. It should be injected from the
	// environment by the bootstrap layer, never committed.
	APIKey string
	// BaseURL optionally overrides the provider's default API endpoint. Useful
	// for proxies, gateways, compatible endpoints (e.g. Azure OpenAI), and for
	// pointing at a local test server. Empty means "use the SDK default".
	BaseURL string
	// Timeout optionally overrides the global RequestTimeout for this provider.
	// A zero value means "inherit the global timeout".
	Timeout time.Duration
	// Models optionally overrides the adapter's built-in list of supported model
	// IDs. Empty means "use the adapter defaults".
	Models []string
}

// ResolvedTimeout returns the provider-specific timeout if set, otherwise the
// supplied global default.
func (p ProviderConfig) ResolvedTimeout(global time.Duration) time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return global
}

// Config is the root configuration for ModelMesh. Only the fields relevant to
// the Provider Layer foundation are present in Phase 1; later phases add their
// own sub-configs (routing weights, cache TTLs, budgets, ...) as additional
// fields.
type Config struct {
	// DefaultProvider is the provider used when a request does not specify one.
	// It may be empty; the Manager will then require an explicit provider per
	// request.
	DefaultProvider string

	// RequestTimeout bounds a single provider request.
	RequestTimeout time.Duration

	// RetryCount is the number of retries an adapter may perform for a single
	// request. Zero means no retries. (Cross-provider fallback is a separate,
	// future concern owned by the orchestrator, not this value.)
	RetryCount int

	// HealthCheckInterval is how often background health checks run. The Provider
	// Layer foundation does not schedule checks yet; this value is defined now so
	// the Circuit Breaker / Health Monitor phase has a stable home for it.
	HealthCheckInterval time.Duration

	// Providers is the optional set of provider configurations.
	Providers []ProviderConfig
}

// DefaultConfig returns a Config populated with sensible defaults. It is always
// valid.
func DefaultConfig() Config {
	return Config{
		DefaultProvider:     "",
		RequestTimeout:      DefaultRequestTimeout,
		RetryCount:          DefaultRetryCount,
		HealthCheckInterval: DefaultHealthCheckInterval,
	}
}

// WithDefaults returns a copy of c with any zero-valued fields replaced by their
// defaults. This lets callers supply a partial Config and rely on defaults for
// the rest.
func (c Config) WithDefaults() Config {
	if c.RequestTimeout == 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.HealthCheckInterval == 0 {
		c.HealthCheckInterval = DefaultHealthCheckInterval
	}
	// RetryCount and DefaultProvider have valid zero values, so they are not
	// defaulted here.
	return c
}

// Validate checks the configuration for internal consistency. It returns an
// error wrapping ErrInvalidConfig on the first problem found. Validation is
// intentionally strict so misconfiguration fails fast at startup.
func (c Config) Validate() error {
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("%w: request_timeout must be positive, got %s", ErrInvalidConfig, c.RequestTimeout)
	}
	if c.RetryCount < 0 {
		return fmt.Errorf("%w: retry_count must not be negative, got %d", ErrInvalidConfig, c.RetryCount)
	}
	if c.HealthCheckInterval <= 0 {
		return fmt.Errorf("%w: health_check_interval must be positive, got %s", ErrInvalidConfig, c.HealthCheckInterval)
	}

	seen := make(map[string]struct{}, len(c.Providers))
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("%w: providers[%d].name must not be empty", ErrInvalidConfig, i)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("%w: providers[%d].name %q is duplicated", ErrInvalidConfig, i, p.Name)
		}
		seen[p.Name] = struct{}{}
		if p.Timeout < 0 {
			return fmt.Errorf("%w: providers[%d].timeout must not be negative", ErrInvalidConfig, i)
		}
	}

	// A configured DefaultProvider, if any, should refer to a configured provider
	// when a provider list is supplied. When no provider list is given (common in
	// early phases where providers are registered in code), this check is skipped.
	if c.DefaultProvider != "" && len(c.Providers) > 0 {
		if _, ok := seen[c.DefaultProvider]; !ok {
			return fmt.Errorf("%w: default_provider %q is not among configured providers", ErrInvalidConfig, c.DefaultProvider)
		}
	}

	return nil
}
