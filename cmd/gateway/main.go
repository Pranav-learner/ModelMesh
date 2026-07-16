// Command gateway is the ModelMesh entrypoint.
//
// In Phase 1 Part 1 there is no HTTP server, routing, or real provider — only
// the Provider Layer foundation. This entrypoint therefore does the minimum
// needed to prove the assembled wiring works end to end:
//
//	Application -> Manager -> Registry -> LLMProvider -> Mock Provider
//
// It boots the app with a mock provider, resolves it through the manager, and
// performs a health check and a chat call, logging the outcome. Real provider
// adapters and an HTTP surface arrive in later phases.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/symbiotes/modelmesh/internal/bootstrap"
	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
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

	cfg := config.DefaultConfig()
	cfg.DefaultProvider = "mock"

	// Assemble the Provider Layer foundation with a single mock provider.
	app, err := bootstrap.New(cfg, log, mock.New(mock.WithName("mock")))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), app.Config.RequestTimeout)
	defer cancel()

	// Resolve the default provider through the manager — the deliverable path.
	p, err := app.Manager.Default()
	if err != nil {
		return err
	}

	// Health check.
	health, err := p.HealthCheck(ctx)
	if err != nil {
		return err
	}
	log.Info("provider health",
		logger.String("provider", p.Name()),
		logger.String("state", string(health.State)),
	)

	// A demonstrative chat call through the unified request/response models.
	resp, err := p.Chat(ctx, provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "hello, ModelMesh"},
		},
	})
	if err != nil {
		return err
	}
	log.Info("chat completed",
		logger.String("provider", resp.Provider),
		logger.String("model", resp.Model),
		logger.Int("total_tokens", resp.Usage.TotalTokens),
		logger.String("content", resp.Choices[0].Message.Content),
	)

	return nil
}
