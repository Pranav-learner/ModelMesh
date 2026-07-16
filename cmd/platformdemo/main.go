// Command platformdemo is the complete ModelMesh demonstration: it wires the full
// platform — analysis + adaptive routing, multi-level cache, shadow traffic, and
// the evaluation/experiment engine — fires 100 requests through it, and prints the
// routing decisions, shadow traffic, evaluation results, provider comparison, cost
// savings, latency, similarity, and a final recommendation.
//
//	Application → Intelligent Router → Primary + Shadow → Evaluation → Analytics → Report
//
// Everything is offline and deterministic.
//
// Usage:
//
//	go run ./cmd/platformdemo
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/experiment"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/shadow"
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
				Usage:   provider.Usage{PromptTokens: 400, CompletionTokens: 200, TotalTokens: 600},
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "The answer depends on several factors worth considering."}, FinishReason: provider.FinishReasonStop}},
			}, nil
		}),
	)
}

func main() {
	// --- Providers: a cheap and a premium model, on two providers. ---
	openai := demoProvider("openai", "gpt-4o-mini", "gpt-4")
	anthropic := demoProvider("anthropic", "claude-sonnet", "claude-haiku")

	reg := provider.NewRegistry()
	_ = reg.Register(openai)
	_ = reg.Register(anthropic)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Cost: 0.25, Latency: 0.25, Availability: 0.25, Quality: 0.25}
	rcfg.Weighted.Cost = routing.CostConfig{Pricing: map[string]routing.ModelPricing{
		"gpt-4o-mini": {InputPer1K: 0.0005, OutputPer1K: 0.0015}, "gpt-4": {InputPer1K: 0.03, OutputPer1K: 0.06},
		"claude-haiku": {InputPer1K: 0.0008, OutputPer1K: 0.0024}, "claude-sonnet": {InputPer1K: 0.03, OutputPer1K: 0.06},
	}, EstimatedInputTokens: 400}
	rcfg.Weighted.Quality = routing.QualityConfig{Models: map[string]float64{
		"gpt-4o-mini": 0.60, "gpt-4": 0.95, "claude-haiku": 0.62, "claude-sonnet": 0.97,
	}}
	router, err := routing.Build(pm, rcfg)
	if err != nil {
		fail("router", err)
	}

	// --- Adaptive routing (analysis-driven weights) + classification metrics. ---
	collector := adaptive.NewCollector()
	acfg := adaptive.DefaultConfig()
	acfg.ModelTiers = map[string]analysis.ModelTier{
		"gpt-4o-mini": analysis.TierSmall, "claude-haiku": analysis.TierSmall,
		"gpt-4": analysis.TierLarge, "claude-sonnet": analysis.TierLarge,
	}
	weigher := adaptive.New(acfg, adaptive.WithMetrics(collector))

	// --- Shadow traffic → evaluation engine. ---
	evalEngine := evaluation.New(evaluation.WithCostModel(evaluation.CostModelFunc(func(model string, u provider.Usage) float64 {
		price := map[string]float64{"gpt-4o-mini": 0.001, "gpt-4": 0.05, "claude-haiku": 0.0012, "claude-sonnet": 0.05}
		return float64(u.TotalTokens) / 1000 * price[model]
	})))
	sm, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 25}, // shadow 25% of traffic
		pm, shadow.WithEvaluator(evalEngine), shadow.WithSampler(deterministicSampler()),
	)
	if err != nil {
		fail("shadow", err)
	}

	// --- Multi-level cache (L1) for cache-savings analytics. ---
	l1 := cache.NewMemoryCache(cache.DefaultConfig().Memory)
	cm := cache.NewManager([]cache.Cache{l1})

	gw := gateway.New(router, cm, cache.DefaultConfig(),
		gateway.WithAnalyzer(analysis.New()),
		gateway.WithAdaptiveWeighting(weigher),
		gateway.WithShadow(sm),
		gateway.WithProviderResolver(pm),
		gateway.WithCostEstimator(func(model string, u provider.Usage) float64 {
			price := map[string]float64{"gpt-4o-mini": 0.001, "gpt-4": 0.05, "claude-haiku": 0.0012, "claude-sonnet": 0.05}
			return float64(u.TotalTokens) / 1000 * price[model]
		}),
	)

	// --- Experiment platform. ---
	usage := &usageTally{counts: map[string]int{}}
	em := experiment.NewManager()
	exp, err := em.Create("primary-vs-shadow", "cross-provider cost/quality comparison", evalEngine,
		experiment.WithShadowManager(sm),
		experiment.WithClassification(collector),
		experiment.WithProviderUsage(usage.snapshot),
		experiment.WithCacheSavings(func() float64 { return gw.Stats().CostSavedUSD }),
		experiment.WithMonthlyFactor(720), // project the observed sample to ~monthly volume
	)
	if err != nil {
		fail("experiment", err)
	}

	// --- Fire 100 requests: a mix of simple and complex prompts, some repeated
	//     (to exercise the cache). ---
	prompts := demoPrompts()
	fmt.Println("Firing 100 requests through the full ModelMesh platform...")
	var routingLines []string
	for i := 0; i < 100; i++ {
		p := prompts[i%len(prompts)]
		res, err := gw.Chat(context.Background(), provider.ChatRequest{
			Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: p}},
		})
		if err != nil {
			fail("chat", err)
		}
		prov, model := res.Response.Provider, res.Response.Model
		if res.Selection != nil { // the routed model lives on the decision
			prov, model = res.Selection.Selected.Provider, res.Selection.Selected.Model
		}
		usage.record(prov)
		if i < 6 { // capture a few routing decisions for display
			cx := "?"
			if res.Analysis != nil {
				cx = string(res.Analysis.Classification.Complexity)
			}
			tier := ""
			if res.Adaptive != nil {
				tier = " tier=" + string(res.Analysis.Hints.PreferredModelTier)
			}
			routingLines = append(routingLines, fmt.Sprintf("  #%d %-8s%s → %s/%s (cached=%t)", i+1, cx, tier, prov, model, res.Cached))
		}
	}
	sm.Wait() // drain shadow traffic + evaluations

	// ================= REPORT =================
	report := exp.Report()

	section("ROUTING DECISIONS (sample)")
	fmt.Println(strings.Join(routingLines, "\n"))

	section("SHADOW TRAFFIC")
	s := sm.Stats()
	fmt.Printf("  evaluated=%d sampled=%d dispatched=%d succeeded=%d failed=%d\n",
		s.Evaluated, s.Sampled, s.Dispatched, s.Succeeded, s.Failed)

	section("EVALUATION RESULTS")
	fmt.Print(experiment.EvaluationHistory(exp.Records(), 6))

	section("PROVIDER COMPARISON")
	fmt.Printf("  provider usage:    %s\n", formatCounts(report.ProviderUsage))
	fmt.Printf("  provider win rate: %s\n", formatRates(report.ProviderWinRate))
	fmt.Printf("  classification:    %s (avg complexity %.2f)\n", formatCounts(report.ClassificationDistribution), report.AvgComplexity)

	section("COST · LATENCY · SIMILARITY")
	fmt.Printf("  avg cost difference (shadow − primary): $%.5f/req\n", report.AvgCostDifference)
	fmt.Printf("  avg latency difference:                 %s\n", report.AvgLatencyDifference)
	fmt.Printf("  avg response similarity:                %.3f (exact-match %.0f%%)\n", report.AvgSimilarity, report.ExactMatchRate*100)
	fmt.Printf("  cache savings: $%.4f   est. monthly savings: $%.2f\n", report.CacheSavingsUSD, report.EstimatedMonthlySavingsUSD)

	section("FINAL RECOMMENDATION")
	fmt.Printf("  %s\n", report.Recommendation)
}

// --- helpers ---

type usageTally struct {
	mu     sync.Mutex
	counts map[string]int
}

func (u *usageTally) record(provider string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.counts[provider]++
}
func (u *usageTally) snapshot() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]int, len(u.counts))
	for k, v := range u.counts {
		out[k] = v
	}
	return out
}

// deterministicSampler samples ~25% deterministically (every 4th call), so the
// demo is reproducible.
func deterministicSampler() func() float64 {
	var mu sync.Mutex
	i := 0
	seq := []float64{0.10, 0.90, 0.90, 0.90} // only the first of every 4 is < 0.25
	return func() float64 {
		mu.Lock()
		defer mu.Unlock()
		v := seq[i%len(seq)]
		i++
		return v
	}
}

func demoPrompts() []string {
	return []string{
		"What is the capital of France?",
		"Say hello.",
		"List three colors.",
		"```go\nfunc add(a, b int) int { return a + b }\n```\nExplain step by step and prove the complexity, comparing with alternatives.",
		"Analyze the trade-offs between microservices and monoliths and justify a recommendation step by step.",
		"Compute the integral of x^2 and prove your answer.",
		"What time is it in Tokyo?",
		"Summarize the theory of relativity.",
	}
}

func section(title string) { fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title))) }

func formatCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, " ")
}

func formatRates(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%.0f%%", k, m[k]*100)
	}
	return strings.Join(parts, " ")
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
	os.Exit(1)
}
