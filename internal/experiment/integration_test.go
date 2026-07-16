package experiment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/experiment"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func expProvider(name, model, content string, fail error) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithModels(provider.ModelInfo{ID: model, Capabilities: []provider.Capability{provider.CapabilityChat}}),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			if fail != nil {
				return provider.ChatResponse{}, fail
			}
			return provider.ChatResponse{ID: "r", Provider: name, Model: model,
				Usage:   provider.Usage{PromptTokens: 50, CompletionTokens: 50, TotalTokens: 100},
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: content}, FinishReason: provider.FinishReasonStop}}}, nil
		}),
	)
}

// buildPlatform wires a full experimentation pipeline: gateway → primary + shadow
// → evaluation → experiment report.
func buildPlatform(t *testing.T, shadowFail error) (*gateway.Engine, *shadow.Manager, *experiment.Manager, *experiment.Experiment) {
	t.Helper()
	primary := expProvider("openai", "gpt-4", "the answer is 42", nil)
	secondary := expProvider("anthropic", "claude-sonnet", "the answer is 42 exactly", shadowFail)

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

	eval := evaluation.New(evaluation.WithCostModel(evaluation.CostModelFunc(
		func(model string, u provider.Usage) float64 {
			if model == "gpt-4" {
				return float64(u.TotalTokens) * 0.01
			}
			return float64(u.TotalTokens) * 0.001
		})))
	sm, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(func() float64 { return 0 }), shadow.WithEvaluator(eval))
	if err != nil {
		t.Fatal(err)
	}

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false},
		gateway.WithProviderResolver(pm), gateway.WithShadow(sm))

	em := experiment.NewManager()
	exp, err := em.Create("openai-vs-anthropic", "cost/quality shadow comparison", eval,
		experiment.WithShadowManager(sm),
		experiment.WithMonthlyFactor(50),
	)
	if err != nil {
		t.Fatal(err)
	}
	return gw, sm, em, exp
}

func TestIntegration_ShadowEvaluationReporting(t *testing.T) {
	gw, sm, _, exp := buildPlatform(t, nil)

	const n = 10
	for i := 0; i < n; i++ {
		res, err := gw.Chat(context.Background(), provider.ChatRequest{
			Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "question"}}})
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		if res.Response.Provider != "openai" {
			t.Fatalf("app must receive the primary, got %q", res.Response.Provider)
		}
	}
	sm.Wait() // drain shadows + evaluations

	// Shadow traffic + evaluation produced records.
	if got := len(exp.Records()); got != n {
		t.Fatalf("evaluation records = %d, want %d", got, n)
	}

	// Reporting.
	r := exp.Report()
	if r.Comparable != n || r.ShadowSampled != n {
		t.Errorf("report counts: comparable=%d sampled=%d, want %d", r.Comparable, r.ShadowSampled, n)
	}
	// gpt-4 (0.01/tok) primary vs claude (0.001/tok) shadow → shadow cheaper.
	if r.AvgCostDifference >= 0 {
		t.Errorf("avg cost difference = %v, want negative (shadow cheaper)", r.AvgCostDifference)
	}
	if r.AvgSimilarity <= 0 {
		t.Errorf("avg similarity should be > 0")
	}
	if r.Recommendation == "" {
		t.Errorf("report should carry a recommendation")
	}
}

func TestIntegration_FailureIsolation(t *testing.T) {
	// The shadow provider always fails; the primary must be unaffected and the
	// evaluation records the failures without crashing.
	gw, sm, _, exp := buildPlatform(t, errors.New("shadow provider down"))

	const n = 5
	for i := 0; i < n; i++ {
		res, err := gw.Chat(context.Background(), provider.ChatRequest{
			Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "q"}}})
		if err != nil {
			t.Fatalf("primary must succeed despite shadow failure: %v", err)
		}
		if res.Response.Provider != "openai" {
			t.Fatalf("app received %q, want openai", res.Response.Provider)
		}
	}
	sm.Wait()

	records := exp.Records()
	if len(records) != n {
		t.Fatalf("records = %d, want %d", len(records), n)
	}
	for _, rec := range records {
		if rec.Comparable {
			t.Errorf("failed shadow should be non-comparable: %+v", rec)
		}
	}
	// Non-comparable records are excluded from the comparison averages.
	r := exp.Report()
	if r.Comparable != 0 {
		t.Errorf("comparable = %d, want 0 (all shadows failed)", r.Comparable)
	}
	if !contains(r.Recommendation, "insufficient") {
		t.Errorf("recommendation with no comparable data = %q", r.Recommendation)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
