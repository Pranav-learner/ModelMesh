// Command adaptiveroutingdemo demonstrates ModelMesh's intelligent, request-aware
// routing end to end, fully offline: each prompt is analyzed (features + token
// estimate), classified (simple/medium/complex), turned into routing hints, and
// used to adapt the routing weights — so a simple prompt lands on a cheap model
// and a complex prompt on a high-quality one, from one configuration.
//
//	Application → Request Analysis Engine → Adaptive Router → Provider
//
// For each prompt it prints the extracted features, the classification and its
// explanation, the routing hints, the adjusted weights, and the chosen provider
// with the deciding reason.
//
// Usage:
//
//	go run ./cmd/adaptiveroutingdemo
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func demoProvider(name, model string) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithModels(provider.ModelInfo{ID: model, Capabilities: []provider.Capability{provider.CapabilityChat}}),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{
				ID: "r", Provider: name, Model: model,
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok"}}},
				Usage:   provider.Usage{TotalTokens: 100},
			}, nil
		}),
	)
}

func main() {
	// Two models: cheap/fast/lower-quality vs pricey/high-quality.
	openai := demoProvider("openai", "gpt-4o-mini")
	anthropic := demoProvider("anthropic", "claude-sonnet")

	reg := provider.NewRegistry()
	_ = reg.Register(openai)
	_ = reg.Register(anthropic)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Cost: 0.25, Latency: 0.25, Availability: 0.25, Quality: 0.25}
	rcfg.Weighted.Cost = routing.CostConfig{
		Pricing: map[string]routing.ModelPricing{
			"gpt-4o-mini":   {InputPer1K: 0.0005, OutputPer1K: 0.0015},
			"claude-sonnet": {InputPer1K: 0.03, OutputPer1K: 0.06},
		},
		EstimatedInputTokens: 500,
	}
	rcfg.Weighted.Quality = routing.QualityConfig{Models: map[string]float64{"gpt-4o-mini": 0.60, "claude-sonnet": 0.98}}
	router, err := routing.Build(pm, rcfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "router:", err)
		os.Exit(1)
	}

	acfg := adaptive.DefaultConfig()
	acfg.ModelTiers = map[string]analysis.ModelTier{"gpt-4o-mini": analysis.TierSmall, "claude-sonnet": analysis.TierLarge}
	collector := adaptive.NewCollector()
	weigher := adaptive.New(acfg, adaptive.WithMetrics(collector))

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false},
		gateway.WithAnalyzer(analysis.New()),
		gateway.WithAdaptiveWeighting(weigher),
	)

	prompts := []struct{ label, text string }{
		{"Prompt A (expected: simple → gpt-4o-mini)", "What is the capital of France?"},
		{"Prompt B (expected: complex → claude-sonnet)",
			"```python\ndef solve(n): pass\n```\nExplain step by step, prove the time complexity is O(n^2), and compare with mergesort."},
	}

	ctx := context.Background()
	for _, p := range prompts {
		section(p.label)
		res, err := gw.Chat(ctx, provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: p.text}}})
		if err != nil {
			fmt.Fprintln(os.Stderr, "chat:", err)
			os.Exit(1)
		}

		f := res.Analysis.Features
		fmt.Printf("Features: chars=%d words=%d messages=%d instructions=%d reasoning=%d code=%t math=%t structured=%t\n",
			f.CharCount, f.WordCount, f.MessageCount, f.InstructionCount, f.ReasoningIndicatorCount, f.HasCode, f.HasMath, f.HasStructuredData)
		fmt.Printf("Tokens: input=%d expected-output=%d total=%d\n\n",
			res.Analysis.Tokens.InputTokens, res.Analysis.Tokens.ExpectedOutputTokens, res.Analysis.Tokens.EstimatedTotalTokens)

		fmt.Print(adaptive.ExplainClassification(*res.Analysis))
		fmt.Println()
		fmt.Println(adaptive.ExplainRoutingHints(res.Analysis.Hints))
		fmt.Println()
		fmt.Print(adaptive.ExplainAdaptiveWeighting(*res.Adaptive))
		fmt.Println()
		fmt.Print(adaptive.ShowRoutingDecision(res.Selection.Decision))
		fmt.Printf("\n→ Chosen: %s/%s   Reason: %s\n",
			res.Selection.Selected.Provider, res.Selection.Selected.Model, res.Selection.Selected.Reason)
	}

	section("RESOURCE METRICS")
	s := collector.Snapshot()
	fmt.Printf("Requests: %d   Distribution: %v   Avg complexity: %.2f\n", s.Total, s.Distribution, s.AverageComplexity)
	fmt.Printf("Hint usage: %v   Weight changes: %d\n", s.HintUsage, s.WeightChanges)
	fmt.Printf("Routing accuracy: %.0f%% over %d samples\n", s.Accuracy*100, s.AccuracySamples)
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title)))
}
