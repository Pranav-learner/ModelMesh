// Command routingdemo demonstrates the complete ModelMesh Routing Engine end to
// end, fully offline, using mock providers.
//
// Workflow:
//
//	chat request -> routing engine -> evaluate providers -> score breakdown
//	             -> ranking -> select best provider -> dispatch -> response
//	             -> routing explanation + metrics
//
// It clearly shows WHY a provider was selected. No REST API and no real provider
// calls are involved (that is a later phase); this is a Phase 2 demonstration.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	// Two mock providers offering the same model, differentiated by configured
	// pricing, latency, and quality so the router has a real decision to make.
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("openai")))
	_ = reg.Register(mock.New(mock.WithName("anthropic")))
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	cfg := routing.DefaultConfig()
	cfg.Weighted.Factors = routing.FactorWeights{Cost: 0.3, Latency: 0.2, Availability: 0.2, Quality: 0.3}
	cfg.Weighted.Cost = routing.CostConfig{
		Default:              routing.ModelPricing{InputPer1K: 0.005, OutputPer1K: 0.015},
		EstimatedInputTokens: 1200, EstimatedOutputTokens: 400,
	}
	cfg.Weighted.Latency = routing.LatencyConfig{Providers: map[string]time.Duration{
		"openai": 700 * time.Millisecond, "anthropic": 950 * time.Millisecond,
	}}
	cfg.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{
		"openai": 0.95, "anthropic": 0.92,
	}}

	metrics := routing.NewMetricsCollector()
	router, err := routing.Build(pm, cfg,
		routing.WithLogger(logger.New(logger.LevelInfo)),
		routing.WithMetrics(metrics),
	)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "You are ModelMesh."},
			{Role: provider.RoleUser, Content: "Summarize this PDF about distributed systems."},
		},
	}
	rc := routing.ChatContext(req)
	rc.RequestID = "demo-001"

	section("1. Route: evaluate providers, score, rank, select")
	sel, err := router.Select(ctx, rc)
	if err != nil {
		return err
	}

	section("2. Score breakdown and ranking")
	fmt.Print(routing.Explain(sel.Decision))

	section("3. Selection outcome")
	fmt.Println("   " + routing.InspectSelection(sel))

	section("4. Dispatch the request through the selected provider")
	resp, err := sel.Provider.Chat(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("   [%s] model=%s tokens=%d\n   response: %q\n",
		resp.Provider, resp.Model, resp.Usage.TotalTokens, resp.Choices[0].Message.Content)

	section("5. Routing metrics (internal collector)")
	s := metrics.Snapshot()
	fmt.Printf("   decisions=%d selections=%v avg_score=%.3f avg_time=%s fallbacks=%d failed=%d\n",
		s.TotalDecisions, s.SelectionsPerProvider, s.AverageScore, s.AverageDecisionTime, s.FallbackCount, s.FailedAttempts)

	return nil
}

func section(title string) { fmt.Printf("\n=== %s ===\n", title) }
