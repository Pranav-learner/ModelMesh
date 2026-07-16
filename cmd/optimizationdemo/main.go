// Command optimizationdemo demonstrates ModelMesh's resource-optimization layer
// end to end, fully offline: a user sends a stream of requests on an expensive
// model; as their daily budget is drawn down, the gateway automatically downgrades
// to a cheaper model, the router re-selects the best provider for it, and the load
// balancer spreads the calls across provider instances — every request still
// succeeds.
//
//	User → Budget Engine → Routing Engine → Load Balancer → Provider Instance
//
// For each request it prints the chosen model / provider / instance, whether a
// downgrade occurred, the estimated cost, budget remaining, and cost saved.
//
// Usage:
//
//	go run ./cmd/optimizationdemo
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/optimization"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

const (
	userID     = "alice"
	dailyLimit = 0.30 // USD/day — small so the demo reaches the limit quickly
)

func demoProvider(name string, models ...string) *mock.Provider {
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
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "response"}}},
				Usage:   provider.Usage{PromptTokens: 1000, CompletionTokens: 1000, TotalTokens: 2000},
			}, nil
		}),
	)
}

func main() {
	ctx := context.Background()

	// Providers: openai serves the premium model + the cheap one; anthropic serves
	// only the cheap one — so the downgrade can re-route across providers.
	openai := demoProvider("openai", "gpt-4", "gpt-4o-mini")
	anthropic := demoProvider("anthropic", "gpt-4o-mini")

	reg := provider.NewRegistry()
	_ = reg.Register(openai)
	_ = reg.Register(anthropic)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		fail("router", err)
	}

	// Budget: downgrade policy, cheaper default model.
	bm, err := budget.NewManager(budget.Config{
		Policy:       budget.PolicyDowngrade,
		DefaultModel: "gpt-4o-mini",
		Pricing: budget.PricingConfig{Models: map[string]budget.ModelPricing{
			"gpt-4":       {InputPer1K: 0.03, OutputPer1K: 0.06},     // ~$0.09 / request here
			"gpt-4o-mini": {InputPer1K: 0.0005, OutputPer1K: 0.0015}, // ~$0.002 / request
		}},
	})
	if err != nil {
		fail("budget", err)
	}
	_ = bm.SetBudget(budget.UserBudget(userID, dailyLimit))

	// Load balancer: three openai instances + one anthropic instance.
	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyRoundRobin}, loadbalancer.NewRoundRobin())
	_ = lb.Register(loadbalancer.Instance{ID: "openai-us-east-1", Provider: "openai", Region: "us-east-1", Client: openai})
	_ = lb.Register(loadbalancer.Instance{ID: "openai-eu-west-1", Provider: "openai", Region: "eu-west-1", Client: openai})
	_ = lb.Register(loadbalancer.Instance{ID: "openai-us-west-2", Provider: "openai", Region: "us-west-2", Client: openai})
	_ = lb.Register(loadbalancer.Instance{ID: "anthropic-us-east-1", Provider: "anthropic", Region: "us-east-1", Client: anthropic})

	opt := optimization.New(router, optimization.WithBudget(bm), optimization.WithLoadBalancer(lb))
	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false}, gateway.WithOptimizer(opt))

	fmt.Printf("User %q daily budget: $%.2f   (gpt-4 ≈ $0.09/req, gpt-4o-mini ≈ $0.002/req)\n\n", userID, dailyLimit)
	fmt.Printf("%-4s %-12s %-10s %-20s %-11s %-10s %-10s\n", "#", "MODEL", "PROVIDER", "INSTANCE", "DOWNGRADED", "REMAINING", "SAVED")
	fmt.Println(strings.Repeat("-", 90))

	prompt := longPrompt()
	for i := 1; i <= 12; i++ {
		req := provider.ChatRequest{
			Model:     "gpt-4",
			MaxTokens: 1000,
			Messages:  []provider.ChatMessage{{Role: provider.RoleUser, Content: prompt}},
			Metadata:  map[string]string{gateway.MetaBudgetScope: "user", gateway.MetaBudgetID: userID},
		}
		res, err := gw.Chat(ctx, req)
		if err != nil {
			// A reject policy would land here; the downgrade policy keeps serving.
			fmt.Printf("%-4d REJECTED: %v\n", i, err)
			continue
		}
		st, _ := bm.Budget(budget.ScopeUser, userID)
		p := res.Optimization
		down := "no"
		if p.Downgraded {
			down = "→ yes"
		}
		fmt.Printf("%-4d %-12s %-10s %-20s %-11s $%-9.4f $%-9.4f\n",
			i, p.Model, p.Provider, p.InstanceID(), down, st.Remaining, p.EstimatedSavings)
	}

	// Resource usage summary.
	u := opt.ResourceUsage()
	fmt.Printf("\nResource usage: %d requests, %d downgrades, %d rejects, ~$%.4f saved, $%.4f spent\n",
		u.Requests, u.Downgrades, u.Rejects, u.EstimatedSavingsUSD, u.TotalSpentUSD)

	fmt.Println("\nLoad distribution across instances:")
	for _, inst := range u.Instances {
		fmt.Printf("  %-20s %s  requests=%d\n", inst.ID, inst.Region, inst.RequestCount)
	}

	fmt.Println("\nFinal budget:")
	st, _ := bm.Budget(budget.ScopeUser, userID)
	fmt.Printf("  limit=$%.2f used=$%.4f remaining=$%.4f resets=%s\n",
		st.DailyLimit, st.CurrentUsage, st.Remaining, st.ResetAt.Format("2006-01-02 15:04 MST"))
}

// longPrompt returns a ~1000-token (≈4000 char) prompt so per-request cost is
// large enough to exercise the budget within a few requests.
func longPrompt() string {
	return strings.Repeat("Explain the architecture of a distributed system in detail. ", 70)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
