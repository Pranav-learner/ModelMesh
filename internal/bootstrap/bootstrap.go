// Package bootstrap wires ModelMesh's components together. It is the composition
// root: the one place that knows about config, logging, the registry, and the
// manager at once, and assembles them via dependency injection. Keeping this
// wiring isolated here keeps every other package free of global state and easy
// to test in isolation.
//
// In Phase 1 the bootstrap assembles only the Provider Layer foundation. Later
// phases extend App with their own components (router, cache, ...) constructed
// here from the same Config.
package bootstrap

import (
	"fmt"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// App holds the assembled, ready-to-use components of ModelMesh. It is the
// object a main function (or a future HTTP server) depends on.
type App struct {
	Config   config.Config
	Logger   logger.Logger
	Registry *provider.Registry
	Manager  *provider.Manager
}

// New validates the configuration, builds the provider registry, registers the
// supplied providers, and constructs the manager over them.
//
// Providers are passed in explicitly (rather than being discovered) so that the
// composition root stays the single source of truth for what is wired, and so
// that tests can inject mocks. The logger may be nil, in which case a no-op
// logger is used.
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
