package optimization_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/optimization"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// --- harness ---------------------------------------------------------------

func chatModel(id string) provider.ModelInfo {
	return provider.ModelInfo{ID: id, Capabilities: []provider.Capability{provider.CapabilityChat}}
}

// testProvider builds a mock provider advertising the given models.
func testProvider(name string, models ...string) *mock.Provider {
	infos := make([]provider.ModelInfo, len(models))
	for i, m := range models {
		infos[i] = chatModel(m)
	}
	return mock.New(
		mock.WithName(name),
		mock.WithModels(infos...),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			return provider.ChatResponse{
				ID: "r", Provider: name, Model: req.Model,
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok"}}},
				Usage:   provider.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500},
			}, nil
		}),
	)
}

func newRouter(t *testing.T, providers ...*mock.Provider) *routing.Manager {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.Name(), err)
		}
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider(providers[0].Name()))
	cfg := routing.DefaultConfig()
	r, err := routing.Build(pm, cfg)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	return r
}

func testPricing() budget.PricingConfig {
	return budget.PricingConfig{
		Models: map[string]budget.ModelPricing{
			"gpt-4":       {InputPer1K: 0.03, OutputPer1K: 0.06},
			"gpt-4o-mini": {InputPer1K: 0.0005, OutputPer1K: 0.0015},
		},
		Default: budget.ModelPricing{InputPer1K: 0.001, OutputPer1K: 0.002},
	}
}

func newBudget(t *testing.T, policy, defaultModel string) *budget.Manager {
	t.Helper()
	m, err := budget.NewManager(budget.Config{
		Policy:       policy,
		DefaultModel: defaultModel,
		Pricing:      testPricing(),
	})
	if err != nil {
		t.Fatalf("budget manager: %v", err)
	}
	return m
}

func chatReq(model, prompt string) provider.ChatRequest {
	return provider.ChatRequest{
		Model:    model,
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: prompt}},
	}
}

// --- Load Balancer + Router ------------------------------------------------

func TestOptimize_LoadBalancerAndRouter(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyRoundRobin}, loadbalancer.NewRoundRobin())
	_ = lb.Register(loadbalancer.Instance{ID: "openai-a", Provider: "openai", Region: "us-east-1"})
	_ = lb.Register(loadbalancer.Instance{ID: "openai-b", Provider: "openai", Region: "eu-west-1"})

	o := optimization.New(router, optimization.WithLoadBalancer(lb))

	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		plan, err := o.Optimize(context.Background(), optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi")})
		if err != nil {
			t.Fatalf("optimize: %v", err)
		}
		if plan.Provider != "openai" {
			t.Fatalf("provider = %q, want openai", plan.Provider)
		}
		if plan.InstanceID() == "" {
			t.Fatalf("no instance selected")
		}
		seen[plan.InstanceID()]++
	}
	if seen["openai-a"] != 2 || seen["openai-b"] != 2 {
		t.Errorf("round-robin distribution = %v, want 2/2", seen)
	}
}

// --- Budget + Router -------------------------------------------------------

func TestOptimize_BudgetAllowsWithinLimit(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	bm := newBudget(t, budget.PolicyReject, "")
	_ = bm.SetBudget(budget.UserBudget("u1", 100))
	o := optimization.New(router, optimization.WithBudget(bm))

	plan, err := o.Optimize(context.Background(), optimization.OptimizeRequest{
		Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 1000, ExpectedOutputTokens: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Allowed() || plan.Rejected {
		t.Fatalf("plan should be allowed")
	}
	if plan.Budget == nil || plan.Budget.Outcome != budget.OutcomeAllow {
		t.Errorf("budget outcome = %v, want allow", plan.Budget)
	}
	if plan.EstimatedCost <= 0 {
		t.Errorf("estimated cost not set")
	}
}

func TestOptimize_BudgetRejectsWhenExhausted(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	bm := newBudget(t, budget.PolicyReject, "")
	_ = bm.SetBudget(budget.UserBudget("u1", 0.01))
	o := optimization.New(router, optimization.WithBudget(bm))
	ctx := context.Background()

	// Exhaust the budget.
	_ = bm.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.01})

	plan, err := o.Optimize(ctx, optimization.OptimizeRequest{
		Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 1000, ExpectedOutputTokens: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Allowed() || !plan.Rejected {
		t.Errorf("exhausted budget should reject: %+v", plan)
	}
}

// --- Downgrade Flow --------------------------------------------------------

func TestOptimize_DowngradeReRoutesToCheaperModel(t *testing.T) {
	// openai serves gpt-4; both providers serve gpt-4o-mini (the downgrade target).
	router := newRouter(t, testProvider("openai", "gpt-4", "gpt-4o-mini"), testProvider("anthropic", "gpt-4o-mini"))
	bm := newBudget(t, budget.PolicyDowngrade, "gpt-4o-mini")
	_ = bm.SetBudget(budget.UserBudget("u1", 1.0))
	o := optimization.New(router, optimization.WithBudget(bm))
	ctx := context.Background()

	// Leave 0.2 remaining; gpt-4 (1K*0.03 + 1K*0.06 = 0.09)... make it not fit by
	// spending down to 0.05.
	_ = bm.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.95})

	plan, err := o.Optimize(ctx, optimization.OptimizeRequest{
		Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 1000, ExpectedOutputTokens: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Downgraded {
		t.Fatalf("expected downgrade, got %+v", plan)
	}
	if plan.Model != "gpt-4o-mini" || plan.OriginalModel != "gpt-4" {
		t.Errorf("downgraded to %s (from %s), want gpt-4o-mini", plan.Model, plan.OriginalModel)
	}
	if plan.EstimatedSavings <= 0 {
		t.Errorf("estimated savings = %v, want > 0", plan.EstimatedSavings)
	}
	// The re-routed provider must actually advertise the downgraded model.
	if plan.Provider != "openai" && plan.Provider != "anthropic" {
		t.Errorf("re-routed to unexpected provider %q", plan.Provider)
	}
}

// --- Multiple Provider Instances -------------------------------------------

func TestOptimize_MultipleInstancesPerProvider(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyLeastLatency}, loadbalancer.NewLeastLatency())
	for _, id := range []string{"a", "b", "c"} {
		_ = lb.Register(loadbalancer.Instance{ID: "openai-" + id, Provider: "openai"})
	}
	o := optimization.New(router, optimization.WithLoadBalancer(lb))
	ctx := context.Background()

	// Prime latencies: b fastest.
	_ = lb.Update(loadbalancer.Observation{InstanceID: "openai-a", Latency: 100 * time.Millisecond})
	_ = lb.Update(loadbalancer.Observation{InstanceID: "openai-b", Latency: 10 * time.Millisecond})
	_ = lb.Update(loadbalancer.Observation{InstanceID: "openai-c", Latency: 50 * time.Millisecond})

	plan, err := o.Optimize(ctx, optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstanceID() != "openai-b" {
		t.Errorf("least-latency selected %q, want openai-b", plan.InstanceID())
	}

	// Commit feeds latency back to the balancer.
	if err := o.Commit(ctx, plan, provider.Usage{TotalTokens: 100}, 12*time.Millisecond, true); err != nil {
		t.Fatal(err)
	}
	stats := lb.Statistics()
	var total uint64
	for _, s := range stats.Instances {
		total += s.RequestCount
	}
	if total == 0 {
		t.Errorf("expected instance request counts after selection")
	}
}

// --- Concurrent Requests ---------------------------------------------------

func TestOptimize_ConcurrentRequests(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	bm := newBudget(t, budget.PolicyReject, "")
	_ = bm.SetBudget(budget.TeamBudget("team-1", 1_000_000))
	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyRoundRobin}, loadbalancer.NewRoundRobin())
	_ = lb.Register(loadbalancer.Instance{ID: "openai-a", Provider: "openai"})
	_ = lb.Register(loadbalancer.Instance{ID: "openai-b", Provider: "openai"})
	o := optimization.New(router, optimization.WithBudget(bm), optimization.WithLoadBalancer(lb))

	const workers, per = 16, 50
	var wg sync.WaitGroup
	var ok int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < per; i++ {
				plan, err := o.Optimize(ctx, optimization.OptimizeRequest{
					Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeTeam, BudgetID: "team-1", InputTokens: 100, ExpectedOutputTokens: 100,
				})
				if err != nil || !plan.Allowed() {
					t.Errorf("optimize: err=%v allowed=%v", err, plan.Allowed())
					return
				}
				if err := o.Commit(ctx, plan, provider.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}, 5*time.Millisecond, true); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				atomic.AddInt64(&ok, 1)
			}
		}()
	}
	wg.Wait()

	if ok != int64(workers*per) {
		t.Fatalf("completed %d, want %d", ok, workers*per)
	}
	usage := o.ResourceUsage()
	if usage.Requests != int64(workers*per) {
		t.Errorf("requests = %d, want %d", usage.Requests, workers*per)
	}
	// Every request committed 100 in / 100 out tokens on gpt-4 (0.03/0.06 per 1K):
	// 0.1K*0.03 + 0.1K*0.06 = 0.009 each.
	wantSpent := float64(workers*per) * 0.009
	if usage.TotalSpentUSD < wantSpent-1e-6 || usage.TotalSpentUSD > wantSpent+1e-6 {
		t.Errorf("total spent = %v, want ~%v", usage.TotalSpentUSD, wantSpent)
	}
}
