package loadbalancer

import "fmt"

// StrategyBuilder constructs a Strategy from configuration. New algorithms
// register a builder here (or via a custom Registry) to become nameable in
// config without any change to the balancer.
type StrategyBuilder func(cfg Config) (Strategy, error)

// Registry maps strategy names to builders. It is the extension seam: adding a
// strategy is registering a builder, not editing a switch.
type Registry struct {
	builders map[string]StrategyBuilder
}

// NewRegistry returns an empty strategy registry.
func NewRegistry() *Registry {
	return &Registry{builders: make(map[string]StrategyBuilder)}
}

// Register adds a builder under name, overwriting any existing entry.
func (r *Registry) Register(name string, b StrategyBuilder) {
	r.builders[name] = b
}

// Build instantiates the named strategy. It returns ErrStrategyNotImplemented for
// recognized-but-reserved names and ErrUnknownStrategy for unrecognized ones, so
// callers can distinguish "coming later" from "typo".
func (r *Registry) Build(name string, cfg Config) (Strategy, error) {
	if b, ok := r.builders[name]; ok {
		return b(cfg)
	}
	if isReserved(name) {
		return nil, fmt.Errorf("%w: %q", ErrStrategyNotImplemented, name)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownStrategy, name)
}

// DefaultRegistry returns a registry with the implemented strategies registered.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(StrategyRoundRobin, func(Config) (Strategy, error) { return NewRoundRobin(), nil })
	r.Register(StrategyLeastLatency, func(Config) (Strategy, error) { return NewLeastLatency(), nil })
	return r
}
