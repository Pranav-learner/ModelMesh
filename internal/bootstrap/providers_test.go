package bootstrap_test

import (
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
)

func TestProvidersFromConfig_BuildsKnownProviders(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{
		{Name: "openai", Enabled: true, APIKey: "k1"},
		{Name: "anthropic", Enabled: true, APIKey: "k2"},
	}

	providers, err := bootstrap.ProvidersFromConfig(cfg)
	if err != nil {
		t.Fatalf("ProvidersFromConfig() = %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("built %d providers, want 2", len(providers))
	}

	names := map[string]bool{}
	for _, p := range providers {
		names[p.Name()] = true
	}
	if !names["openai"] || !names["anthropic"] {
		t.Errorf("missing expected providers: %v", names)
	}
}

func TestProvidersFromConfig_SkipsDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{
		{Name: "openai", Enabled: true, APIKey: "k1"},
		{Name: "anthropic", Enabled: false, APIKey: "k2"},
	}

	providers, err := bootstrap.ProvidersFromConfig(cfg)
	if err != nil {
		t.Fatalf("ProvidersFromConfig() = %v", err)
	}
	if len(providers) != 1 || providers[0].Name() != "openai" {
		t.Errorf("expected only openai, got %d providers", len(providers))
	}
}

func TestProvidersFromConfig_UnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{{Name: "gemini", Enabled: true}}

	_, err := bootstrap.ProvidersFromConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("ProvidersFromConfig(unknown) = %v, want unknown provider error", err)
	}
}

// TestProvidersFromConfig_RegistersThroughManager verifies the full Part 1 +
// Part 2 path: config -> adapters -> registry -> manager retrieval.
func TestProvidersFromConfig_RegistersThroughManager(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "openai"
	cfg.Providers = []config.ProviderConfig{
		{Name: "openai", Enabled: true, APIKey: "k1"},
		{Name: "anthropic", Enabled: true, APIKey: "k2"},
	}

	providers, err := bootstrap.ProvidersFromConfig(cfg)
	if err != nil {
		t.Fatalf("ProvidersFromConfig() = %v", err)
	}

	app, err := bootstrap.New(cfg, nil, providers...)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	for _, name := range []string{"openai", "anthropic"} {
		p, err := app.Manager.Provider(name)
		if err != nil {
			t.Errorf("Manager.Provider(%q) = %v", name, err)
			continue
		}
		if p.Name() != name {
			t.Errorf("resolved %q, want %q", p.Name(), name)
		}
	}

	def, err := app.Manager.Default()
	if err != nil || def.Name() != "openai" {
		t.Errorf("Default() = %v, %v; want openai", def, err)
	}
}
