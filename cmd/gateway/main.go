// Command gateway is the ModelMesh entrypoint.
//
// Phase 1 has no HTTP server, routing, or cache — only the Provider Layer. This
// entrypoint runs the complete startup flow and exercises the deliverable path:
//
//	Application -> Bootstrap -> Factory -> Registry -> Manager -> LLMProvider
//	                                                               ├── OpenAIProvider   -> OpenAI SDK
//	                                                               └── AnthropicProvider -> Anthropic SDK
//
// Real providers are configured from environment credentials (OPENAI_API_KEY,
// ANTHROPIC_API_KEY) and built by the factory. When none are present, a mock
// provider is registered so the binary still demonstrates the wiring fully
// offline.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/anthropic"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/openai"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	log := logger.New(logger.LevelInfo)

	app, err := initApp(log)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), app.Config.RequestTimeout)
	defer cancel()

	if err := app.Initialize(ctx); err != nil {
		return err
	}
	defer func() { _ = app.Shutdown(context.Background()) }()

	// Exercise the deliverable path against the default provider.
	p, err := app.Manager.DefaultProvider()
	if err != nil {
		return err
	}

	if health, err := p.HealthCheck(ctx); err != nil {
		log.Warn("health check could not complete", logger.String("provider", p.Name()), logger.Err(err))
	} else {
		log.Info("provider health",
			logger.String("provider", health.Provider),
			logger.String("state", string(health.State)),
			logger.String("latency", health.Latency.String()),
		)
	}

	resp, err := p.Chat(ctx, provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hello, ModelMesh"}},
	})
	if err != nil {
		log.Warn("chat call failed", logger.String("provider", p.Name()), logger.Err(err))
		return nil
	}
	log.Info("chat completed",
		logger.String("provider", resp.Provider),
		logger.String("model", resp.Model),
		logger.Int("total_tokens", resp.Usage.TotalTokens),
		logger.String("content", resp.Choices[0].Message.Content),
	)
	return nil
}

// initApp builds the App from environment configuration. If provider credentials
// are present it runs the full factory-based Bootstrap; otherwise it falls back
// to a mock provider via New so the gateway remains runnable offline.
func initApp(log logger.Logger) (*bootstrap.App, error) {
	cfg := config.DefaultConfig()

	var pcs []config.ProviderConfig
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		pcs = append(pcs, config.ProviderConfig{Name: openai.ProviderName, Enabled: true, APIKey: key})
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		pcs = append(pcs, config.ProviderConfig{Name: anthropic.ProviderName, Enabled: true, APIKey: key})
	}

	if len(pcs) == 0 {
		log.Info("no provider credentials found; using mock provider")
		cfg.DefaultProvider = "mock"
		return bootstrap.New(cfg, log, mock.New(mock.WithName("mock")))
	}

	cfg.Providers = pcs
	cfg.DefaultProvider = pcs[0].Name
	return bootstrap.Bootstrap(cfg, log)
}
