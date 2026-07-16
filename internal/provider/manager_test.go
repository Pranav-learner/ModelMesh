package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func newManager(t *testing.T, defaultProvider string, providers ...provider.LLMProvider) *provider.Manager {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Name(), err)
		}
	}
	return provider.NewManager(reg, provider.WithDefaultProvider(defaultProvider))
}

func TestManager_ProviderByName(t *testing.T) {
	m := newManager(t, "", mock.New(mock.WithName("openai")))

	p, err := m.Provider("openai")
	if err != nil {
		t.Fatalf("Provider(openai) = %v, want nil", err)
	}
	if p.Name() != "openai" {
		t.Errorf("resolved name = %q, want %q", p.Name(), "openai")
	}
}

func TestManager_ProviderNotFound(t *testing.T) {
	m := newManager(t, "", mock.New(mock.WithName("openai")))

	_, err := m.Provider("anthropic")
	if !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("Provider(anthropic) = %v, want ErrProviderNotFound", err)
	}
}

func TestManager_DefaultResolution(t *testing.T) {
	m := newManager(t, "openai", mock.New(mock.WithName("openai")))

	// Empty name resolves to the default.
	p, err := m.Provider("")
	if err != nil {
		t.Fatalf("Provider(\"\") = %v, want nil", err)
	}
	if p.Name() != "openai" {
		t.Errorf("default resolved name = %q, want %q", p.Name(), "openai")
	}

	// Default() is equivalent.
	p2, err := m.Default()
	if err != nil {
		t.Fatalf("Default() = %v", err)
	}
	if p2.Name() != "openai" {
		t.Errorf("Default().Name() = %q, want %q", p2.Name(), "openai")
	}
}

func TestManager_NoDefaultConfigured(t *testing.T) {
	m := newManager(t, "", mock.New(mock.WithName("openai")))

	_, err := m.Provider("")
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("Provider(\"\") with no default = %v, want ErrInvalidRequest", err)
	}
}

func TestManager_DefaultNotRegistered(t *testing.T) {
	// Default provider name is set but not registered.
	m := newManager(t, "ghost", mock.New(mock.WithName("openai")))

	_, err := m.Default()
	if !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("Default() with missing provider = %v, want ErrProviderNotFound", err)
	}
}

func TestManager_ExistsAndNames(t *testing.T) {
	m := newManager(t, "",
		mock.New(mock.WithName("bravo")),
		mock.New(mock.WithName("alpha")),
	)

	if !m.Exists("alpha") || !m.Exists("bravo") {
		t.Errorf("Exists returned false for a registered provider")
	}
	if m.Exists("charlie") {
		t.Errorf("Exists(charlie) = true, want false")
	}

	names := m.Names()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "bravo" {
		t.Errorf("Names() = %v, want [alpha bravo]", names)
	}
}

func TestManager_Describe(t *testing.T) {
	p := mock.New(
		mock.WithName("openai"),
		mock.WithModels(
			provider.ModelInfo{ID: "gpt", Capabilities: []provider.Capability{provider.CapabilityChat}},
			provider.ModelInfo{ID: "emb", Capabilities: []provider.Capability{provider.CapabilityEmbeddings}},
		),
	)
	m := newManager(t, "", p)

	info, err := m.Describe(context.Background(), "openai")
	if err != nil {
		t.Fatalf("Describe(openai) = %v", err)
	}
	if info.Name != "openai" {
		t.Errorf("info.Name = %q, want openai", info.Name)
	}
	if !info.Capabilities.Chat || !info.Capabilities.Embeddings {
		t.Errorf("capabilities = %+v, want chat+embeddings", info.Capabilities)
	}
	if info.Capabilities.Streaming {
		t.Errorf("Streaming should be false in phase 1")
	}
	if len(info.Models) != 2 {
		t.Errorf("len(Models) = %d, want 2", len(info.Models))
	}
}

func TestManager_DescribeUnknownProvider(t *testing.T) {
	m := newManager(t, "", mock.New(mock.WithName("openai")))

	_, err := m.Describe(context.Background(), "nope")
	if !errors.Is(err, provider.ErrProviderNotFound) {
		t.Fatalf("Describe(nope) = %v, want ErrProviderNotFound", err)
	}
}

func TestManager_DiscoveryAPI(t *testing.T) {
	m := newManager(t, "openai",
		mock.New(mock.WithName("openai"), mock.WithModels(
			provider.ModelInfo{ID: "gpt", Capabilities: []provider.Capability{provider.CapabilityChat}},
		)),
		mock.New(mock.WithName("anthropic")),
	)
	ctx := context.Background()

	if names := m.ListProviders(); len(names) != 2 || names[0] != "anthropic" || names[1] != "openai" {
		t.Errorf("ListProviders() = %v, want sorted [anthropic openai]", names)
	}

	p, err := m.GetProvider("openai")
	if err != nil || p.Name() != "openai" {
		t.Errorf("GetProvider() = %v, %v", p, err)
	}

	def, err := m.DefaultProvider()
	if err != nil || def.Name() != "openai" {
		t.Errorf("DefaultProvider() = %v, %v", def, err)
	}

	models, err := m.ListModels(ctx, "openai")
	if err != nil || len(models) != 1 || models[0].ID != "gpt" {
		t.Errorf("ListModels() = %v, %v", models, err)
	}

	caps, err := m.ProviderCapabilities(ctx, "openai")
	if err != nil || !caps.Chat {
		t.Errorf("ProviderCapabilities() = %+v, %v", caps, err)
	}

	if _, err := m.ListModels(ctx, "ghost"); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Errorf("ListModels(ghost) = %v, want ErrProviderNotFound", err)
	}
}
