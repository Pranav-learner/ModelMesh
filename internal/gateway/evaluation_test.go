package gateway_test

import (
	"context"
	"testing"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func evalProvider(name, model, content string) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithModels(provider.ModelInfo{ID: model, Capabilities: []provider.Capability{provider.CapabilityChat}}),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{ID: "r", Provider: name, Model: model,
				Usage:   provider.Usage{PromptTokens: 50, CompletionTokens: 50, TotalTokens: 100},
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: content}, FinishReason: provider.FinishReasonStop}}}, nil
		}),
	)
}

func TestGatewayEvaluation_EndToEnd(t *testing.T) {
	primary := evalProvider("openai", "gpt-4", "the answer is 42")
	secondary := evalProvider("anthropic", "claude-sonnet", "the answer is 42 indeed")

	reg := provider.NewRegistry()
	if err := reg.Register(primary); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(secondary); err != nil {
		t.Fatal(err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	// Wire the evaluation engine into the shadow manager.
	engine := evaluation.New(evaluation.WithCostModel(evaluation.CostModelFunc(
		func(model string, u provider.Usage) float64 {
			if model == "gpt-4" {
				return float64(u.TotalTokens) * 0.01
			}
			return float64(u.TotalTokens) * 0.001
		})))
	sm, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm,
		shadow.WithSampler(func() float64 { return 0 }),
		shadow.WithEvaluator(engine),
	)
	if err != nil {
		t.Fatal(err)
	}

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false},
		gateway.WithProviderResolver(pm),
		gateway.WithShadow(sm),
	)

	// Fire requests; the app receives only the primary.
	const n = 4
	for i := 0; i < n; i++ {
		res, err := gw.Chat(context.Background(), provider.ChatRequest{
			Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Response.Provider != "openai" {
			t.Fatalf("application received %q, want primary openai", res.Response.Provider)
		}
	}
	sm.Wait() // drain shadows + their evaluations

	// The evaluation engine recorded a comparison per request.
	records := engine.Records()
	if len(records) != n {
		t.Fatalf("evaluation records = %d, want %d", len(records), n)
	}
	r := records[0]
	if !r.Comparable {
		t.Fatalf("record should be comparable: %+v", r)
	}
	if r.Comparison.PrimaryProvider != "openai" || r.Comparison.ShadowProvider != "anthropic" {
		t.Errorf("comparison sides = %s vs %s", r.Comparison.PrimaryProvider, r.Comparison.ShadowProvider)
	}
	// gpt-4 (0.01/token) vs claude (0.001/token), equal tokens → shadow cheaper.
	if !r.Comparison.Cost.ShadowCheaper {
		t.Errorf("shadow should be cheaper: %+v", r.Comparison.Cost)
	}
	// "the answer is 42" vs "the answer is 42 indeed" → high but not exact similarity.
	if r.Comparison.Quality.ExactMatch || r.Comparison.Quality.TextSimilarity <= 0 {
		t.Errorf("similarity metrics wrong: %+v", r.Comparison.Quality)
	}

	stats := engine.Statistics()
	if stats.Comparable != n {
		t.Errorf("statistics comparable = %d, want %d", stats.Comparable, n)
	}
	if _, ok := stats.ProviderWinRate["anthropic"]; !ok {
		t.Errorf("expected provider win rate for anthropic: %+v", stats.ProviderWinRate)
	}
}
