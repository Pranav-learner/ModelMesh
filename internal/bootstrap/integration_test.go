package bootstrap_test

// This file contains end-to-end integration tests for the complete Provider
// Layer: configuration -> factory -> registry -> manager -> providers, plus the
// App lifecycle. Real adapters are exercised against httptest servers; no live
// API is contacted.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/openai"
	"github.com/symbiotes/modelmesh/internal/provider/factory"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// --- helpers ----------------------------------------------------------------

func mockNamedBuilder(name string, opts ...mock.Option) bootstrap.NamedBuilder {
	return bootstrap.NamedBuilder{
		Name: name,
		Builder: func(pc config.ProviderConfig, _ factory.Deps) (provider.LLMProvider, error) {
			return mock.New(append([]mock.Option{mock.WithName(pc.Name)}, opts...)...), nil
		},
	}
}

const oaiChatBody = `{"id":"chatcmpl-1","object":"chat.completion","created":1700000000,"model":"gpt-4o",
"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

const oaiModelsBody = `{"object":"list","data":[{"id":"gpt-4o","object":"model","created":1,"owned_by":"openai"}]}`

func oaiErr(msg string) string {
	return `{"error":{"message":"` + msg + `","type":"x","code":"x"}}`
}

// openAINamedBuilder registers a custom-named OpenAI adapter pointed at baseURL,
// so integration tests can exercise the real adapter over httptest.
func openAINamedBuilder(name, baseURL string, strict bool) bootstrap.NamedBuilder {
	return bootstrap.NamedBuilder{
		Name: name,
		Builder: func(pc config.ProviderConfig, d factory.Deps) (provider.LLMProvider, error) {
			return openai.New(openai.Config{
				Name:         pc.Name,
				APIKey:       pc.APIKey,
				BaseURL:      baseURL,
				StrictModels: strict,
				Retry:        retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
			}), nil
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// --- tests ------------------------------------------------------------------

func TestIntegration_BootstrapDiscoveryAndSwitching(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "mock-a"
	cfg.Providers = []config.ProviderConfig{
		{Name: "mock-a", Enabled: true},
		{Name: "mock-b", Enabled: true},
	}

	app, err := bootstrap.Bootstrap(cfg, nil, mockNamedBuilder("mock-a"), mockNamedBuilder("mock-b"))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	// Discovery.
	if got := app.Manager.ListProviders(); len(got) != 2 || got[0] != "mock-a" || got[1] != "mock-b" {
		t.Errorf("ListProviders() = %v, want [mock-a mock-b]", got)
	}
	def, err := app.Manager.DefaultProvider()
	if err != nil || def.Name() != "mock-a" {
		t.Errorf("DefaultProvider() = %v, %v", def, err)
	}
	models, err := app.Manager.ListModels(context.Background(), "mock-a")
	if err != nil || len(models) == 0 {
		t.Errorf("ListModels() = %v, %v", models, err)
	}
	caps, err := app.Manager.ProviderCapabilities(context.Background(), "mock-a")
	if err != nil || !caps.Chat {
		t.Errorf("ProviderCapabilities() = %+v, %v", caps, err)
	}

	// Switching + delegation: identical request, resolved via different providers.
	req := provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}}
	for _, name := range []string{"mock-a", "mock-b"} {
		p, err := app.Manager.GetProvider(name)
		if err != nil {
			t.Fatalf("GetProvider(%q) = %v", name, err)
		}
		resp, err := p.Chat(context.Background(), req)
		if err != nil {
			t.Fatalf("Chat via %q = %v", name, err)
		}
		if resp.Provider != name {
			t.Errorf("response served by %q, want %q", resp.Provider, name)
		}
	}
}

func TestIntegration_Lifecycle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "mock-a"
	cfg.Providers = []config.ProviderConfig{{Name: "mock-a", Enabled: true}}

	app, err := bootstrap.Bootstrap(cfg, nil, mockNamedBuilder("mock-a"))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	ctx := context.Background()
	if err := app.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() = %v", err)
	}
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() = %v", err)
	}

	p, _ := app.Manager.GetProvider("mock-a")
	m := p.(*mock.Provider)
	if m.InitializeCalls() != 1 || m.ShutdownCalls() != 1 {
		t.Errorf("lifecycle calls = init:%d shutdown:%d, want 1/1", m.InitializeCalls(), m.ShutdownCalls())
	}
}

func TestIntegration_InitializeFailsFast(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{{Name: "boom", Enabled: true}}

	app, err := bootstrap.Bootstrap(cfg, nil, mockNamedBuilder("boom", mock.WithInitError(errors.New("no init"))))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
	if err := app.Initialize(context.Background()); err == nil {
		t.Fatalf("Initialize() = nil, want error from failing provider")
	}
}

func TestIntegration_HealthCheckAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, oaiModelsBody)
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "oai"
	cfg.Providers = []config.ProviderConfig{
		{Name: "oai", Enabled: true, APIKey: "k"},
		{Name: "mk", Enabled: true},
	}

	app, err := bootstrap.Bootstrap(cfg, nil,
		openAINamedBuilder("oai", srv.URL, false),
		mockNamedBuilder("mk"),
	)
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	statuses := app.HealthCheckAll(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("HealthCheckAll returned %d statuses, want 2", len(statuses))
	}
	if statuses["oai"].State != provider.HealthStateHealthy {
		t.Errorf("oai health = %+v, want healthy", statuses["oai"])
	}
	if statuses["mk"].State != provider.HealthStateHealthy {
		t.Errorf("mk health = %+v", statuses["mk"])
	}
}

func TestIntegration_RetryThroughManager(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			writeJSON(w, http.StatusServiceUnavailable, oaiErr("later"))
			return
		}
		writeJSON(w, 200, oaiChatBody)
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "oai"
	cfg.Providers = []config.ProviderConfig{{Name: "oai", Enabled: true, APIKey: "k"}}

	app, err := bootstrap.Bootstrap(cfg, nil, openAINamedBuilder("oai", srv.URL, false))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	p, _ := app.Manager.DefaultProvider()
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Chat() = %v, want success after retries", err)
	}
	if resp.Choices[0].Message.Content != "pong" || calls != 3 {
		t.Errorf("content=%q calls=%d, want pong/3", resp.Choices[0].Message.Content, calls)
	}
}

func TestIntegration_ErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnauthorized, oaiErr("bad key"))
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "oai"
	cfg.Providers = []config.ProviderConfig{{Name: "oai", Enabled: true, APIKey: "k"}}

	app, err := bootstrap.Bootstrap(cfg, nil, openAINamedBuilder("oai", srv.URL, false))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	p, _ := app.Manager.DefaultProvider()
	_, err = p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, provider.ErrAuthenticationFailed) {
		t.Fatalf("Chat() = %v, want ErrAuthenticationFailed propagated through the layer", err)
	}
}

func TestIntegration_UnsupportedModel(t *testing.T) {
	// Strict OpenAI adapter should reject a model absent from its catalog, before
	// any network call (server fails the test if contacted).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream call for unsupported model: %s", r.URL.Path)
		writeJSON(w, 200, oaiChatBody)
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "oai"
	cfg.Providers = []config.ProviderConfig{{Name: "oai", Enabled: true, APIKey: "k"}}

	app, err := bootstrap.Bootstrap(cfg, nil, openAINamedBuilder("oai", srv.URL, true))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}

	p, _ := app.Manager.DefaultProvider()
	_, err = p.Chat(context.Background(), provider.ChatRequest{
		Model:    "nonexistent-model",
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, provider.ErrUnsupportedModel) {
		t.Fatalf("Chat(bad model) = %v, want ErrUnsupportedModel", err)
	}
}

func TestIntegration_InvalidProviderLookup(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{{Name: "mock-a", Enabled: true}}

	app, err := bootstrap.Bootstrap(cfg, nil, mockNamedBuilder("mock-a"))
	if err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
	if _, err := app.Manager.GetProvider("does-not-exist"); !errors.Is(err, provider.ErrProviderNotFound) {
		t.Errorf("GetProvider(missing) = %v, want ErrProviderNotFound", err)
	}
}

// --- startup validation -----------------------------------------------------

func TestIntegration_StartupValidation(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Config
		extra []bootstrap.NamedBuilder
	}{
		{
			name: "missing API key",
			cfg: config.Config{Providers: []config.ProviderConfig{
				{Name: "openai", Enabled: true}, // no APIKey
			}},
		},
		{
			name: "unknown provider",
			cfg: config.Config{Providers: []config.ProviderConfig{
				{Name: "gemini", Enabled: true, APIKey: "k"},
			}},
		},
		{
			name: "invalid base URL",
			cfg: config.Config{Providers: []config.ProviderConfig{
				{Name: "openai", Enabled: true, APIKey: "k", BaseURL: "not a url"},
			}},
		},
		{
			name: "duplicate provider",
			cfg: config.Config{Providers: []config.ProviderConfig{
				{Name: "openai", Enabled: true, APIKey: "k"},
				{Name: "openai", Enabled: true, APIKey: "k"},
			}},
		},
		{
			name: "default provider not configured",
			cfg: config.Config{
				DefaultProvider: "anthropic",
				Providers:       []config.ProviderConfig{{Name: "openai", Enabled: true, APIKey: "k"}},
			},
		},
		{
			name: "invalid timeout",
			cfg:  config.Config{RequestTimeout: -1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, err := bootstrap.Bootstrap(tt.cfg, nil, tt.extra...)
			if err == nil {
				t.Fatalf("Bootstrap() = nil error, want failure; app=%v", app)
			}
			if app != nil {
				t.Errorf("Bootstrap() returned non-nil App on failure (partial init)")
			}
		})
	}
}

func TestIntegration_NoModelsIsRejected(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers = []config.ProviderConfig{{Name: "empty", Enabled: true}}

	// A builder producing a provider with an empty catalog must fail startup.
	_, err := bootstrap.Bootstrap(cfg, nil, bootstrap.NamedBuilder{
		Name: "empty",
		Builder: func(pc config.ProviderConfig, _ factory.Deps) (provider.LLMProvider, error) {
			return mock.New(mock.WithName("empty"), mock.WithModels()), nil
		},
	})
	if err == nil {
		t.Fatalf("Bootstrap() = nil, want error for provider advertising no models")
	}
}
