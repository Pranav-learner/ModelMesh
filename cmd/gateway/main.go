// Command gateway is the ModelMesh entrypoint.
//
// Phase 1 has no HTTP server, routing, or cache — only the Provider Layer. This
// entrypoint assembles the provider layer and exercises the deliverable path:
//
//	Application -> Provider Manager -> Provider Registry -> LLMProvider
//	                                                          ├── OpenAIProvider   -> OpenAI SDK
//	                                                          └── AnthropicProvider -> Anthropic SDK
//
// Real providers are configured from environment credentials (OPENAI_API_KEY,
// ANTHROPIC_API_KEY). When none are present, a mock provider is registered so
// the binary still demonstrates the wiring fully offline.
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

	cfg, providers, err := loadProviders(log)
	if err != nil {
		return err
	}

	app, err := bootstrap.New(cfg, log, providers...)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), app.Config.RequestTimeout)
	defer cancel()

	// Exercise the deliverable path against the default provider.
	p, err := app.Manager.Default()
	if err != nil {
		return err
	}

	health, err := p.HealthCheck(ctx)
	if err != nil {
		log.Warn("health check could not complete", logger.String("provider", p.Name()), logger.Err(err))
	} else {
		log.Info("provider health",
			logger.String("provider", health.Provider),
			logger.String("state", string(health.State)),
			logger.String("latency", health.Latency.String()),
		)
	}

	resp, err := p.Chat(ctx, provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "hello, ModelMesh"},
		},
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

// loadProviders builds the configuration and provider set from the environment.
// If no provider credentials are present, it falls back to a mock provider so
// the gateway remains runnable offline.
func loadProviders(log logger.Logger) (config.Config, []provider.LLMProvider, error) {
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
		return cfg, []provider.LLMProvider{mock.New(mock.WithName("mock"))}, nil
	}

	cfg.Providers = pcs
	cfg.DefaultProvider = pcs[0].Name

	providers, err := bootstrap.ProvidersFromConfig(cfg)
	if err != nil {
		return config.Config{}, nil, err
	}
	return cfg, providers, nil
}
