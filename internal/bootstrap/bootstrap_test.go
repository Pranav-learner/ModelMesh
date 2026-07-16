package bootstrap_test

import (
	"errors"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func TestBootstrap_WiresProviderLayer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "mock"

	app, err := bootstrap.New(cfg, nil, mock.New(mock.WithName("mock")))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	if app.Manager == nil || app.Registry == nil {
		t.Fatalf("app not fully wired: %+v", app)
	}
	if app.Registry.Len() != 1 {
		t.Errorf("Registry.Len() = %d, want 1", app.Registry.Len())
	}

	p, err := app.Manager.Default()
	if err != nil {
		t.Fatalf("Default() = %v", err)
	}
	if p.Name() != "mock" {
		t.Errorf("default provider = %q, want mock", p.Name())
	}
}

func TestBootstrap_AppliesDefaults(t *testing.T) {
	// A zero Config should be defaulted and pass validation.
	app, err := bootstrap.New(config.Config{}, nil)
	if err != nil {
		t.Fatalf("New(zero config) = %v", err)
	}
	if app.Config.RequestTimeout != config.DefaultRequestTimeout {
		t.Errorf("defaults not applied: RequestTimeout = %s", app.Config.RequestTimeout)
	}
}

func TestBootstrap_InvalidConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.RequestTimeout = -1 * time.Second

	_, err := bootstrap.New(cfg, nil)
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("New(invalid) = %v, want ErrInvalidConfig", err)
	}
}

func TestBootstrap_DuplicateProviderFails(t *testing.T) {
	_, err := bootstrap.New(config.DefaultConfig(), nil,
		mock.New(mock.WithName("dup")),
		mock.New(mock.WithName("dup")),
	)
	if !errors.Is(err, provider.ErrProviderExists) {
		t.Fatalf("New(dup providers) = %v, want ErrProviderExists", err)
	}
}
