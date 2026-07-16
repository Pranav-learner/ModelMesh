// Command demo demonstrates the completed ModelMesh Provider Layer end to end,
// fully offline and deterministically, using mock providers.
//
// It exercises the full subsystem exactly as a future phase would:
//
//	Bootstrap -> lifecycle Initialize -> discover providers -> list models
//	          -> switch provider -> send a chat request -> print the normalized
//	          response -> health check -> lifecycle Shutdown
//
// No REST API and no real provider calls are involved; this is a demonstration
// harness for Phase 1, not a production entrypoint (that is cmd/gateway).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/factory"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	log := logger.New(logger.LevelInfo)

	// Two mock providers so the demo can show discovery and switching offline.
	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "mock-primary"
	cfg.Providers = []config.ProviderConfig{
		{Name: "mock-primary", Enabled: true},
		{Name: "mock-secondary", Enabled: true},
	}

	app, err := bootstrap.Bootstrap(cfg, log,
		mockBuilder("mock-primary"),
		mockBuilder("mock-secondary"),
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Lifecycle: initialize, and guarantee shutdown.
	if err := app.Initialize(ctx); err != nil {
		return err
	}
	defer func() { _ = app.Shutdown(context.Background()) }()

	section("1. Discover providers")
	for _, name := range app.Manager.ListProviders() {
		info, err := app.Manager.Describe(ctx, name)
		if err != nil {
			return err
		}
		fmt.Printf("   • %-16s chat=%v embeddings=%v models=%d\n",
			info.Name, info.Capabilities.Chat, info.Capabilities.Embeddings, len(info.Models))
	}

	section("2. List models for the default provider")
	def, err := app.Manager.DefaultProvider()
	if err != nil {
		return err
	}
	models, err := app.Manager.ListModels(ctx, def.Name())
	if err != nil {
		return err
	}
	for _, m := range models {
		fmt.Printf("   • %s (%v)\n", m.ID, m.Capabilities)
	}

	section("3. Send a chat request via the default provider, then switch")
	req := provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "You are ModelMesh."},
			{Role: provider.RoleUser, Content: "Say hello."},
		},
	}
	for _, name := range app.Manager.ListProviders() {
		p, err := app.Manager.GetProvider(name)
		if err != nil {
			return err
		}
		resp, err := p.Chat(ctx, req)
		if err != nil {
			return err
		}
		fmt.Printf("   [%s] model=%s tokens=%d content=%q\n",
			resp.Provider, resp.Model, resp.Usage.TotalTokens, resp.Choices[0].Message.Content)
	}

	section("4. Health check all providers")
	for name, h := range app.HealthCheckAll(ctx) {
		fmt.Printf("   • %-16s state=%s latency=%s\n", name, h.State, h.Latency)
	}

	section("5. Shutdown (via deferred lifecycle Shutdown)")
	fmt.Println("   releasing provider resources...")
	return nil
}

// mockBuilder returns a NamedBuilder producing a deterministic mock provider.
func mockBuilder(name string) bootstrap.NamedBuilder {
	return bootstrap.NamedBuilder{
		Name: name,
		Builder: func(pc config.ProviderConfig, _ factory.Deps) (provider.LLMProvider, error) {
			return mock.New(mock.WithName(pc.Name)), nil
		},
	}
}

func section(title string) {
	fmt.Printf("\n=== %s ===\n", title)
}
