package gateway_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/optimization"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func optProvider(name string, models ...string) *mock.Provider {
	infos := make([]provider.ModelInfo, len(models))
	for i, m := range models {
		infos[i] = provider.ModelInfo{ID: m, Capabilities: []provider.Capability{provider.CapabilityChat}}
	}
	return mock.New(
		mock.WithName(name),
		mock.WithModels(infos...),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{
				ID: "r", Provider: name, Model: req.Model,
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok"}}},
				Usage:   provider.Usage{PromptTokens: 1000, CompletionTokens: 1000, TotalTokens: 2000},
			}, nil
		}),
	)
}

// optFixture wires a full optimized gateway: router + downgrade budget + LB with
// instances carrying provider clients.
type optFixture struct {
	gw     *gateway.Engine
	budget *budget.Manager
}

func newOptFixture(t *testing.T, policy, defaultModel string, limit float64) *optFixture {
	t.Helper()
	oa := optProvider("openai", "gpt-4", "gpt-4o-mini")

	reg := provider.NewRegistry()
	if err := reg.Register(oa); err != nil {
		t.Fatal(err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	bm, err := budget.NewManager(budget.Config{
		Policy: policy, DefaultModel: defaultModel,
		Pricing: budget.PricingConfig{Models: map[string]budget.ModelPricing{
			"gpt-4":       {InputPer1K: 0.03, OutputPer1K: 0.06},
			"gpt-4o-mini": {InputPer1K: 0.0005, OutputPer1K: 0.0015},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = bm.SetBudget(budget.UserBudget("u1", limit))

	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyRoundRobin}, loadbalancer.NewRoundRobin())
	// Two instances of openai, both dispatchable via the same client.
	_ = lb.Register(loadbalancer.Instance{ID: "openai-a", Provider: "openai", Region: "us-east-1", Client: oa})
	_ = lb.Register(loadbalancer.Instance{ID: "openai-b", Provider: "openai", Region: "eu-west-1", Client: oa})

	opt := optimization.New(router, optimization.WithBudget(bm), optimization.WithLoadBalancer(lb))
	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false}, gateway.WithOptimizer(opt))
	return &optFixture{gw: gw, budget: bm}
}

func optReq(model, prompt string) provider.ChatRequest {
	return provider.ChatRequest{
		Model:    model,
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: prompt}},
		Metadata: map[string]string{gateway.MetaBudgetScope: "user", gateway.MetaBudgetID: "u1"},
	}
}

func TestGatewayOptimized_AllowsAndSelectsInstance(t *testing.T) {
	f := newOptFixture(t, budget.PolicyDowngrade, "gpt-4o-mini", 100)
	res, err := f.gw.Chat(context.Background(), optReq("gpt-4", "hello"))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if res.Optimization == nil {
		t.Fatal("expected optimization plan on result")
	}
	if res.Optimization.Downgraded {
		t.Errorf("request within budget should not downgrade")
	}
	if res.Optimization.InstanceID() == "" {
		t.Errorf("expected an instance to be selected")
	}
	if res.Response.Provider != "openai" {
		t.Errorf("served by %q, want openai", res.Response.Provider)
	}
}

func TestGatewayOptimized_DowngradesWhenBudgetLow(t *testing.T) {
	f := newOptFixture(t, budget.PolicyDowngrade, "gpt-4o-mini", 1.0)
	ctx := context.Background()
	// Spend down to 0.05 remaining; gpt-4 (1K*0.03+1K*0.06=0.09) no longer fits.
	_ = f.budget.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.95})

	res, err := f.gw.Chat(ctx, optReq("gpt-4", "hello"))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !res.Optimization.Downgraded {
		t.Fatalf("expected downgrade, got %+v", res.Optimization)
	}
	if res.Optimization.Model != "gpt-4o-mini" || res.Response.Model != "gpt-4o-mini" {
		t.Errorf("served model plan=%s response=%s, want gpt-4o-mini", res.Optimization.Model, res.Response.Model)
	}
	if res.Optimization.EstimatedSavings <= 0 {
		t.Errorf("expected positive estimated savings")
	}
}

func TestGatewayOptimized_RejectsWhenExhausted(t *testing.T) {
	f := newOptFixture(t, budget.PolicyReject, "", 0.01)
	ctx := context.Background()
	_ = f.budget.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.01})

	_, err := f.gw.Chat(ctx, optReq("gpt-4", "hello"))
	if !errors.Is(err, optimization.ErrBudgetExceeded) {
		t.Errorf("exhausted budget = %v, want ErrBudgetExceeded", err)
	}
}

func TestGatewayOptimized_ConcurrentRequests(t *testing.T) {
	f := newOptFixture(t, budget.PolicyReject, "", 1_000_000)
	const workers, per = 12, 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	instances := map[string]int{}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				res, err := f.gw.Chat(context.Background(), optReq("gpt-4", "hello"))
				if err != nil {
					t.Errorf("concurrent chat: %v", err)
					return
				}
				mu.Lock()
				instances[res.Optimization.InstanceID()]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	total := instances["openai-a"] + instances["openai-b"]
	if total != workers*per {
		t.Errorf("served %d, want %d", total, workers*per)
	}
	// Round robin should exercise both instances.
	if instances["openai-a"] == 0 || instances["openai-b"] == 0 {
		t.Errorf("both instances should be used: %v", instances)
	}
}
