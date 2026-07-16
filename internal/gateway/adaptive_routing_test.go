package gateway_test

import (
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// newAdaptiveGateway wires analysis + adaptive weighting + a weighted router over
// two models: a cheap/fast/lower-quality one and a pricey/high-quality one. With
// request-aware weighting, simple prompts should route to the cheap model and
// complex prompts to the high-quality one — from one configuration.
func newAdaptiveGateway(t *testing.T, collector adaptive.Metrics) *gateway.Engine {
	t.Helper()
	openai := optProvider("openai", "gpt-4o-mini")         // cheap, fast, lower quality
	anthropic := optProvider("anthropic", "claude-sonnet") // pricey, high quality

	reg := provider.NewRegistry()
	if err := reg.Register(openai); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(anthropic); err != nil {
		t.Fatal(err)
	}
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
	rcfg.Weighted.Quality = routing.QualityConfig{Models: map[string]float64{
		"gpt-4o-mini": 0.60, "claude-sonnet": 0.98,
	}}
	router, err := routing.Build(pm, rcfg)
	if err != nil {
		t.Fatal(err)
	}

	acfg := adaptive.DefaultConfig()
	acfg.ModelTiers = map[string]analysis.ModelTier{"gpt-4o-mini": analysis.TierSmall, "claude-sonnet": analysis.TierLarge}
	wopts := []adaptive.Option{}
	if collector != nil {
		wopts = append(wopts, adaptive.WithMetrics(collector))
	}

	return gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false},
		gateway.WithAnalyzer(analysis.New()),
		gateway.WithAdaptiveWeighting(adaptive.New(acfg, wopts...)),
	)
}

// req builds a chat request that lets routing choose the model (empty Model).
func req(prompt string) provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: prompt}}}
}

func TestAdaptive_SimplePrompt(t *testing.T) {
	gw := newAdaptiveGateway(t, nil)
	res, err := gw.Chat(context.Background(), req("What is the capital of France?"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Analysis.Classification.Complexity != analysis.ComplexitySimple {
		t.Errorf("complexity = %s, want simple", res.Analysis.Classification.Complexity)
	}
	if res.Selection.Selected.Model != "gpt-4o-mini" {
		t.Errorf("simple prompt routed to %q, want gpt-4o-mini (cheap)", res.Response.Model)
	}
	if res.Adaptive == nil || !res.Adaptive.Changed() {
		t.Errorf("expected adaptive weighting to be applied")
	}
}

func TestAdaptive_ComplexPrompt(t *testing.T) {
	gw := newAdaptiveGateway(t, nil)
	res, err := gw.Chat(context.Background(), req(
		"```python\ndef solve(n): pass\n```\nExplain step by step, prove the time complexity is O(n^2), and compare with mergesort."))
	if err != nil {
		t.Fatal(err)
	}
	if res.Analysis.Classification.Complexity != analysis.ComplexityComplex {
		t.Errorf("complexity = %s, want complex", res.Analysis.Classification.Complexity)
	}
	if res.Selection.Selected.Model != "claude-sonnet" {
		t.Errorf("complex prompt routed to %q, want claude-sonnet (quality)", res.Response.Model)
	}
	if !res.Analysis.Hints.ReasoningIntensive {
		t.Errorf("complex code+math+reasoning should be reasoning-intensive")
	}
}

func TestAdaptive_RoutingAdaptation(t *testing.T) {
	// The SAME gateway routes simple vs complex to different models.
	gw := newAdaptiveGateway(t, nil)
	simple, _ := gw.Chat(context.Background(), req("Say hello."))
	complex, _ := gw.Chat(context.Background(), req(
		"```go\nfunc f(n int) int { return n }\n```\nProve the complexity is O(n^2), analyze the trade-offs, and explain step by step."))

	if simple.Selection.Selected.Model == complex.Selection.Selected.Model {
		t.Errorf("adaptation failed: both routed to %q", simple.Selection.Selected.Model)
	}
	if simple.Selection.Selected.Model != "gpt-4o-mini" || complex.Selection.Selected.Model != "claude-sonnet" {
		t.Errorf("adaptation wrong: simple=%q complex=%q", simple.Selection.Selected.Model, complex.Selection.Selected.Model)
	}
}

func TestAdaptive_CodeGeneration(t *testing.T) {
	gw := newAdaptiveGateway(t, nil)
	res, _ := gw.Chat(context.Background(), req("```go\nfunc add(a, b int) int { return a + b }\n```\nAdd unit tests."))
	if !res.Analysis.Features.HasCode {
		t.Errorf("code prompt should detect code")
	}
	if res.Analysis.Classification.Complexity == analysis.ComplexitySimple {
		t.Errorf("code generation should not be simple")
	}
}

func TestAdaptive_MathematicalQuery(t *testing.T) {
	gw := newAdaptiveGateway(t, nil)
	res, _ := gw.Chat(context.Background(), req("Compute the integral of x^2 from 0 to 5 and prove your answer."))
	if !res.Analysis.Features.HasMath {
		t.Errorf("math prompt should detect math")
	}
	if !res.Analysis.Hints.ReasoningIntensive {
		t.Errorf("math query should be reasoning-intensive")
	}
}

func TestAdaptive_LongContext(t *testing.T) {
	gw := newAdaptiveGateway(t, nil)
	long := "Summarize the following document.\n" + strings.Repeat("This is a long background document with many details. ", 400)
	res, _ := gw.Chat(context.Background(), req(long))
	if !res.Analysis.Hints.HighContext && !res.Analysis.Hints.LongContext {
		t.Errorf("long prompt should be flagged high/long context (input tokens %d)", res.Analysis.Tokens.InputTokens)
	}
	// The high-context signal raises the quality weight.
	if res.Adaptive == nil || res.Adaptive.Adjusted.Quality <= res.Adaptive.Base.Quality-0.001 {
		if res.Analysis.Hints.HighContext {
			t.Errorf("high-context should not lower quality weight: %+v", res.Adaptive)
		}
	}
}

func TestAdaptive_MetricsCollected(t *testing.T) {
	col := adaptive.NewCollector()
	gw := newAdaptiveGateway(t, col)
	ctx := context.Background()
	_, _ = gw.Chat(ctx, req("Hi"))
	_, _ = gw.Chat(ctx, req("Explain step by step and prove why ```go\nx:=1\n``` compiles, comparing approaches."))

	snap := col.Snapshot()
	if snap.Total != 2 {
		t.Errorf("classified %d requests, want 2", snap.Total)
	}
	if snap.WeightChanges == 0 {
		t.Errorf("expected recorded weight changes")
	}
	if snap.AccuracySamples == 0 {
		t.Errorf("expected routing-accuracy samples (tier map configured)")
	}
}
