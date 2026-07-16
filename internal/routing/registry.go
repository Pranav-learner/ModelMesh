package routing

import (
	"fmt"
	"sort"
)

// Builder constructs a Strategy from routing configuration. Each strategy reads
// the section of Config it needs (the weighted builder reads cfg.Weighted).
type Builder func(cfg Config) (Strategy, error)

// Registry maps strategy names to their builders. It is the strategy analogue of
// the provider factory: adding a strategy is registering one builder, with no
// change to the router. It is intended for sequential setup, not concurrent
// mutation.
type Registry struct {
	builders map[string]Builder
}

// NewRegistry returns an empty strategy registry.
func NewRegistry() *Registry {
	return &Registry{builders: make(map[string]Builder)}
}

// DefaultRegistry returns a registry pre-registered with the strategies
// implemented in this phase. Currently that is the weighted strategy; the
// reserved names (round-robin, random, cost-first, latency-first) are handled by
// Build with a not-implemented error rather than being registered.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	// Registration of a built-in cannot fail on an empty registry; ignore error.
	_ = r.Register(StrategyWeighted, func(cfg Config) (Strategy, error) {
		return NewWeighted(cfg.Weighted), nil
	})
	return r
}

// Register associates a builder with a strategy name.
func (r *Registry) Register(name string, b Builder) error {
	if name == "" {
		return fmt.Errorf("routing: strategy name must not be empty")
	}
	if b == nil {
		return fmt.Errorf("routing: builder for %q must not be nil", name)
	}
	if _, exists := r.builders[name]; exists {
		return fmt.Errorf("routing: strategy %q already registered", name)
	}
	r.builders[name] = b
	return nil
}

// Supports reports whether a builder is registered for name.
func (r *Registry) Supports(name string) bool {
	_, ok := r.builders[name]
	return ok
}

// Names returns the sorted names of registered strategies.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.builders))
	for name := range r.builders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Build constructs the named strategy from cfg. Unregistered names yield
// ErrStrategyNotImplemented for recognized reserved names, and ErrUnknownStrategy
// otherwise, so callers get an actionable message either way.
func (r *Registry) Build(name string, cfg Config) (Strategy, error) {
	b, ok := r.builders[name]
	if !ok {
		if isReserved(name) {
			return nil, fmt.Errorf("%w: %q", ErrStrategyNotImplemented, name)
		}
		return nil, fmt.Errorf("%w: %q", ErrUnknownStrategy, name)
	}
	s, err := b(cfg)
	if err != nil {
		return nil, fmt.Errorf("routing: build strategy %q: %w", name, err)
	}
	if s == nil {
		return nil, fmt.Errorf("routing: builder for %q returned nil strategy", name)
	}
	return s, nil
}
