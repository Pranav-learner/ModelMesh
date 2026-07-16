// Package bootstrap wires ModelMesh's components together. It is the composition
// root: the one place that knows about config, logging, the provider factory,
// the registry, and the manager at once, and assembles them via dependency
// injection. Keeping this wiring isolated here keeps every other package free of
// global state and easy to test in isolation.
//
// Two entry points are provided:
//
//   - Bootstrap performs the complete startup flow: validate configuration,
//     build providers from configuration via the factory, register them, create
//     the manager, and run startup validation. This is what production callers
//     use.
//   - New is the lower-level constructor that registers already-built providers.
//     Bootstrap uses it internally; tests and the offline demo use it directly to
//     wire mock providers.
//
// In Phase 1 the bootstrap assembles only the Provider Layer. Later phases extend
// App with their own components (router, cache, ...) constructed here from the
// same Config.
package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/factory"
)

// App holds the assembled, ready-to-use components of ModelMesh. It is the object
// a main function (or a future HTTP server) depends on, and it owns the Provider
// Layer's lifecycle.
type App struct {
	Config   config.Config
	Logger   logger.Logger
	Registry *provider.Registry
	Manager  *provider.Manager
	// Factory is the provider factory used to build the registered providers. It
	// is nil when providers were supplied directly to New.
	Factory *factory.Factory
}

// Bootstrap performs the complete Provider Layer startup flow and returns a
// fully initialized App, or an error. It never returns a partially initialized
// system: any failure (invalid config, provider build failure, failed startup
// validation) aborts and returns nil.
//
// The optional builders allow callers (and tests) to extend the default factory
// with additional providers without modifying this package.
func Bootstrap(cfg config.Config, log logger.Logger, extra ...NamedBuilder) (*App, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if log == nil {
		log = logger.Nop()
	}

	f := DefaultFactory(factory.Deps{
		Logger:         log,
		DefaultTimeout: cfg.RequestTimeout,
		RetryCount:     cfg.RetryCount,
	})
	for _, nb := range extra {
		if err := f.Register(nb.Name, nb.Builder); err != nil {
			return nil, fmt.Errorf("bootstrap: register custom provider: %w", err)
		}
	}

	providers, err := f.BuildAll(cfg)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	app, err := New(cfg, log, providers...)
	if err != nil {
		return nil, err
	}
	app.Factory = f

	if err := app.validateStartup(context.Background()); err != nil {
		return nil, err
	}
	return app, nil
}

// NamedBuilder pairs a provider name with its factory builder, used to extend the
// default factory through Bootstrap.
type NamedBuilder struct {
	Name    string
	Builder factory.BuilderFunc
}

// New validates the configuration, builds the provider registry, registers the
// supplied providers, and constructs the manager over them. It also verifies that
// the configured default provider is registered, failing fast otherwise.
//
// Providers are passed in explicitly so the composition root stays the single
// source of truth for what is wired, and so tests can inject mocks. The logger
// may be nil, in which case a no-op logger is used.
func New(cfg config.Config, log logger.Logger, providers ...provider.LLMProvider) (*App, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	if log == nil {
		log = logger.Nop()
	}

	registry := provider.NewRegistry()
	for _, p := range providers {
		if err := registry.Register(p); err != nil {
			return nil, fmt.Errorf("bootstrap: register provider: %w", err)
		}
		log.Info("registered provider", logger.String("provider", p.Name()))
	}

	if cfg.DefaultProvider != "" && !registry.Exists(cfg.DefaultProvider) {
		return nil, fmt.Errorf("bootstrap: %w: default provider %q is not registered",
			config.ErrInvalidConfig, cfg.DefaultProvider)
	}

	manager := provider.NewManager(
		registry,
		provider.WithDefaultProvider(cfg.DefaultProvider),
		provider.WithLogger(log),
	)

	log.Info("provider layer initialized",
		logger.Int("providers", registry.Len()),
		logger.String("default_provider", cfg.DefaultProvider),
	)

	return &App{
		Config:   cfg,
		Logger:   log,
		Registry: registry,
		Manager:  manager,
	}, nil
}

// Initialize runs the Initialize phase of the lifecycle for every registered
// provider that implements provider.Lifecycle. It fails fast on the first error
// so the system never runs with a half-initialized provider.
func (a *App) Initialize(ctx context.Context) error {
	for _, p := range a.Registry.List() {
		lc, ok := p.(provider.Lifecycle)
		if !ok {
			continue
		}
		if err := lc.Initialize(ctx); err != nil {
			return fmt.Errorf("bootstrap: initialize provider %q: %w", p.Name(), err)
		}
	}
	a.Logger.Info("provider layer ready", logger.Int("providers", a.Registry.Len()))
	return nil
}

// Shutdown runs the Shutdown phase for every registered provider that implements
// provider.Lifecycle, releasing resources. It attempts to shut down every
// provider and returns the combined error, so one failing provider does not
// prevent others from being cleaned up.
func (a *App) Shutdown(ctx context.Context) error {
	var errs []error
	for _, p := range a.Registry.List() {
		lc, ok := p.(provider.Lifecycle)
		if !ok {
			continue
		}
		if err := lc.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown provider %q: %w", p.Name(), err))
		}
	}
	a.Logger.Info("provider layer shut down")
	return errors.Join(errs...)
}

// HealthCheckAll performs an on-demand health check of every registered provider
// and returns the results keyed by provider name. It is a discovery/diagnostic
// helper, not a background monitor (which is a later phase); a failed check is
// reported as an unhealthy status rather than an error.
func (a *App) HealthCheckAll(ctx context.Context) map[string]provider.HealthStatus {
	out := make(map[string]provider.HealthStatus, a.Registry.Len())
	for _, p := range a.Registry.List() {
		h, err := p.HealthCheck(ctx)
		if err != nil {
			h = provider.HealthStatus{
				Provider: p.Name(),
				State:    provider.HealthStateUnhealthy,
				Detail:   err.Error(),
			}
		}
		out[p.Name()] = h
	}
	return out
}

// validateStartup performs the fail-fast checks that require the registered
// providers to exist. Configuration structure is already validated by
// config.Validate; this covers cross-cutting invariants: the default provider is
// registered, and every provider advertises at least one model.
func (a *App) validateStartup(ctx context.Context) error {
	if a.Config.DefaultProvider != "" && !a.Registry.Exists(a.Config.DefaultProvider) {
		return fmt.Errorf("bootstrap: %w: default provider %q is not registered",
			config.ErrInvalidConfig, a.Config.DefaultProvider)
	}
	for _, p := range a.Registry.List() {
		models, err := p.Models(ctx)
		if err != nil {
			return fmt.Errorf("bootstrap: model discovery failed for provider %q: %w", p.Name(), err)
		}
		if len(models) == 0 {
			return fmt.Errorf("bootstrap: %w: provider %q advertises no models",
				config.ErrInvalidConfig, p.Name())
		}
	}
	return nil
}
