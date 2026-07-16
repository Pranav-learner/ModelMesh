package provider

import (
	"context"
	"fmt"

	"github.com/symbiotes/modelmesh/internal/logger"
)

// Manager is the entry point the rest of the application uses to obtain a
// provider. It sits directly above the Registry and is responsible only for
// provider management: resolving a provider by name (or the configured
// default), reporting existence, and describing providers.
//
// It deliberately contains NO routing, scoring, or failover logic. Those are
// later phases that will *wrap* the Manager rather than modify it — the Manager
// is the stable seam they build on.
//
// Dependencies are injected via constructor options (no global state). The zero
// value is not usable; construct with NewManager.
type Manager struct {
	registry        *Registry
	defaultProvider string
	log             logger.Logger
}

// ManagerOption configures a Manager. Options keep the constructor signature
// stable as new dependencies are added in future phases.
type ManagerOption func(*Manager)

// WithDefaultProvider sets the provider name used when a caller does not specify
// one. If unset, Default and Provider("") return an error.
func WithDefaultProvider(name string) ManagerOption {
	return func(m *Manager) { m.defaultProvider = name }
}

// WithLogger injects a structured logger. If unset, a no-op logger is used so
// the Manager never depends on global logging state.
func WithLogger(l logger.Logger) ManagerOption {
	return func(m *Manager) {
		if l != nil {
			m.log = l
		}
	}
}

// NewManager constructs a Manager over the given registry. The registry must be
// non-nil.
func NewManager(registry *Registry, opts ...ManagerOption) *Manager {
	m := &Manager{
		registry: registry,
		log:      logger.Nop(),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Provider resolves a provider by name. An empty name resolves to the configured
// default provider. It returns an error wrapping ErrProviderNotFound if no
// matching provider is registered, or ErrInvalidRequest if an empty name is
// given but no default is configured.
func (m *Manager) Provider(name string) (LLMProvider, error) {
	if name == "" {
		if m.defaultProvider == "" {
			return nil, NewError("", "lookup",
				fmt.Errorf("%w: no provider specified and no default configured", ErrInvalidRequest))
		}
		name = m.defaultProvider
	}

	p, ok := m.registry.Get(name)
	if !ok {
		m.log.Warn("provider lookup failed",
			logger.String("provider", name),
		)
		return nil, NewError(name, "lookup", ErrProviderNotFound)
	}
	return p, nil
}

// Default returns the configured default provider, or an error wrapping
// ErrInvalidRequest if no default is configured (or ErrProviderNotFound if the
// configured default is not registered).
func (m *Manager) Default() (LLMProvider, error) {
	return m.Provider("")
}

// Exists reports whether a provider is registered under name.
func (m *Manager) Exists(name string) bool {
	return m.registry.Exists(name)
}

// Names returns the sorted names of all registered providers.
func (m *Manager) Names() []string {
	return m.registry.Names()
}

// Describe returns a ProviderInfo descriptor for the named provider. It resolves
// the provider and performs model discovery, so it takes a context and is not a
// hot-path call.
func (m *Manager) Describe(ctx context.Context, name string) (ProviderInfo, error) {
	p, err := m.Provider(name)
	if err != nil {
		return ProviderInfo{}, err
	}
	return DescribeProvider(ctx, p)
}
