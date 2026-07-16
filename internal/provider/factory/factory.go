// Package factory constructs provider instances from configuration.
//
// It decouples "which providers exist" from "how the system is wired": each
// provider type registers a builder keyed by its name, and the factory turns a
// configuration into concrete providers by dispatching to those builders. Adding
// a new provider is therefore registering one builder — no existing code path
// changes. This is the mechanism behind the Provider Layer's open/closed
// extensibility.
//
// Builders own provider-specific configuration validation (for example, whether
// an API key is required); the factory owns generic concerns (unknown provider,
// nil results). Structural configuration validation lives in the config package.
package factory

import (
	"fmt"
	"sort"
	"time"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// Deps are the shared dependencies injected into every builder, so builders do
// not reach for global state.
type Deps struct {
	// Logger is the structured logger builders may pass to providers.
	Logger logger.Logger
	// DefaultTimeout is the global per-request timeout a provider inherits when
	// its own timeout is unset.
	DefaultTimeout time.Duration
	// RetryCount is the global retry count applied to provider calls.
	RetryCount int
}

// BuilderFunc constructs a provider from its configuration and the shared deps.
// A builder is responsible for provider-specific validation and returns an error
// (wrapping config.ErrInvalidConfig where appropriate) when the config is
// unusable.
type BuilderFunc func(pc config.ProviderConfig, deps Deps) (provider.LLMProvider, error)

// Factory builds providers from configuration by dispatching to registered
// builders. It is intended for sequential use during startup; it is not designed
// for concurrent mutation.
type Factory struct {
	builders map[string]BuilderFunc
	deps     Deps
}

// New returns an empty Factory carrying the given dependencies. A nil logger is
// replaced with a no-op logger.
func New(deps Deps) *Factory {
	if deps.Logger == nil {
		deps.Logger = logger.Nop()
	}
	return &Factory{
		builders: make(map[string]BuilderFunc),
		deps:     deps,
	}
}

// Register associates a builder with a provider name. It is strict: an empty
// name, nil builder, or duplicate registration is an error, surfacing wiring
// mistakes at startup.
func (f *Factory) Register(name string, b BuilderFunc) error {
	if name == "" {
		return fmt.Errorf("factory: provider name must not be empty")
	}
	if b == nil {
		return fmt.Errorf("factory: builder for %q must not be nil", name)
	}
	if _, exists := f.builders[name]; exists {
		return fmt.Errorf("factory: builder for %q already registered", name)
	}
	f.builders[name] = b
	return nil
}

// Supports reports whether a builder is registered for name.
func (f *Factory) Supports(name string) bool {
	_, ok := f.builders[name]
	return ok
}

// Names returns the sorted names of all registered builders.
func (f *Factory) Names() []string {
	names := make([]string, 0, len(f.builders))
	for name := range f.builders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Build constructs a single provider from its configuration. It returns an error
// for an unknown provider, a builder failure, or a nil result.
func (f *Factory) Build(pc config.ProviderConfig) (provider.LLMProvider, error) {
	b, ok := f.builders[pc.Name]
	if !ok {
		return nil, fmt.Errorf("factory: unknown provider %q (no builder registered)", pc.Name)
	}
	p, err := b(pc, f.deps)
	if err != nil {
		return nil, fmt.Errorf("factory: build %q: %w", pc.Name, err)
	}
	if p == nil {
		return nil, fmt.Errorf("factory: builder for %q returned nil provider", pc.Name)
	}
	return p, nil
}

// BuildAll constructs every enabled provider in cfg, preserving order. Disabled
// entries are skipped. It fails fast on the first build error so the caller never
// receives a partially built set.
func (f *Factory) BuildAll(cfg config.Config) ([]provider.LLMProvider, error) {
	var out []provider.LLMProvider
	for _, pc := range cfg.Providers {
		if !pc.Enabled {
			continue
		}
		p, err := f.Build(pc)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
