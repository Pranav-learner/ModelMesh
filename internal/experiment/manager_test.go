package experiment_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/experiment"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// feed pushes a successful comparison into an evaluation engine.
func feed(e *evaluation.Engine, corr, pModel, sModel string, tokens int, pLat, sLat time.Duration) {
	resp := func(prov, model string) provider.ChatResponse {
		return provider.ChatResponse{Provider: prov, Model: model,
			Usage:   provider.Usage{TotalTokens: tokens},
			Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "answer"}, FinishReason: provider.FinishReasonStop}}}
	}
	e.Evaluate(context.Background(), shadow.Comparison{
		CorrelationID:   corr,
		Primary:         shadow.Target{Provider: "openai", Model: pModel},
		Shadow:          shadow.Target{Provider: "anthropic", Model: sModel},
		PrimaryResponse: resp("openai", pModel),
		PrimaryLatency:  pLat,
		ShadowResult:    shadow.ShadowResult{Success: true, Response: resp("anthropic", sModel), Latency: sLat},
	})
}

// feedFailed pushes a failed shadow comparison into an evaluation engine.
func feedFailed(e *evaluation.Engine, corr, errMsg string) {
	e.Evaluate(context.Background(), shadow.Comparison{
		CorrelationID: corr,
		Primary:       shadow.Target{Provider: "openai", Model: "gpt-4"},
		Shadow:        shadow.Target{Provider: "anthropic", Model: "claude"},
		ShadowResult:  shadow.ShadowResult{Success: false, Err: errMsg},
	})
}

func TestManager_CreateGetList(t *testing.T) {
	m := experiment.NewManager(experiment.WithClock(func() time.Time { return time.Unix(0, 0) }))

	e1, err := m.Create("exp-a", "first", evaluation.New())
	if err != nil {
		t.Fatal(err)
	}
	if e1.Name() != "exp-a" || e1.Description() != "first" {
		t.Errorf("experiment metadata wrong")
	}

	// Duplicate + invalid.
	if _, err := m.Create("exp-a", "", evaluation.New()); !errors.Is(err, experiment.ErrExperimentExists) {
		t.Errorf("dup = %v, want ErrExperimentExists", err)
	}
	if _, err := m.Create("", "", evaluation.New()); !errors.Is(err, experiment.ErrInvalidExperiment) {
		t.Errorf("empty name = %v, want ErrInvalidExperiment", err)
	}
	if _, err := m.Create("exp-b", "", nil); !errors.Is(err, experiment.ErrInvalidExperiment) {
		t.Errorf("nil engine = %v, want ErrInvalidExperiment", err)
	}

	_, _ = m.Create("exp-b", "second", evaluation.New())
	if got := m.List(); len(got) != 2 || got[0].Name() != "exp-a" || got[1].Name() != "exp-b" {
		t.Errorf("list order wrong: %v", got)
	}
	if _, ok := m.Get("exp-a"); !ok {
		t.Errorf("Get should find exp-a")
	}
	if _, err := m.Report("missing"); !errors.Is(err, experiment.ErrExperimentNotFound) {
		t.Errorf("report missing = %v, want ErrExperimentNotFound", err)
	}
}

func TestManager_ReportFromLiveSources(t *testing.T) {
	m := experiment.NewManager()
	eval := evaluation.New(evaluation.WithCostModel(evaluation.CostModelFunc(
		func(model string, u provider.Usage) float64 {
			if model == "expensive" {
				return float64(u.TotalTokens) * 0.01
			}
			return float64(u.TotalTokens) * 0.001
		})))

	col := adaptive.NewCollector()
	col.Classification("simple")
	col.Classification("complex")

	_, err := m.Create("live", "live sources", eval,
		experiment.WithClassification(col),
		experiment.WithCacheSavings(func() float64 { return 2.5 }),
		experiment.WithBudgetSavings(func() float64 { return 1.5 }),
		experiment.WithProviderUsage(func() map[string]int { return map[string]int{"openai": 5, "anthropic": 5} }),
		experiment.WithMonthlyFactor(10),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Two comparable evaluations, shadow cheaper (cheap vs expensive) and identical.
	feed(eval, "r1", "expensive", "cheap", 1000, 800*time.Millisecond, 200*time.Millisecond)
	feed(eval, "r2", "expensive", "cheap", 1000, 700*time.Millisecond, 300*time.Millisecond)

	r, err := m.Report("live")
	if err != nil {
		t.Fatal(err)
	}
	if r.Comparable != 2 {
		t.Errorf("comparable = %d, want 2", r.Comparable)
	}
	if r.ClassificationDistribution["simple"] != 1 || r.ClassificationDistribution["complex"] != 1 {
		t.Errorf("classification distribution = %v", r.ClassificationDistribution)
	}
	if r.ProviderUsage["openai"] != 5 {
		t.Errorf("provider usage = %v", r.ProviderUsage)
	}
	// (2.5 + 1.5) * 10 = 40
	if r.EstimatedMonthlySavingsUSD != 40 {
		t.Errorf("monthly savings = %v, want 40", r.EstimatedMonthlySavingsUSD)
	}
	if r.AvgCostDifference >= 0 {
		t.Errorf("shadow cheaper → negative avg cost diff, got %v", r.AvgCostDifference)
	}
}

func TestManager_ConcurrentExperiments(t *testing.T) {
	m := experiment.NewManager()

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			eval := evaluation.New()
			name := fmt.Sprintf("exp-%02d", i)
			if _, err := m.Create(name, "", eval); err != nil {
				t.Errorf("create %s: %v", name, err)
				return
			}
			feed(eval, "c", "m", "m", 100, time.Second, time.Second)
			// Report concurrently while others are still being created.
			if _, err := m.Report(name); err != nil {
				t.Errorf("report %s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()

	if got := len(m.List()); got != n {
		t.Fatalf("experiments = %d, want %d", got, n)
	}
	reports := m.Reports()
	if len(reports) != n {
		t.Errorf("reports = %d, want %d", len(reports), n)
	}
	for _, r := range reports {
		if r.Comparable != 1 {
			t.Errorf("experiment %s comparable = %d, want 1", r.Experiment, r.Comparable)
		}
	}
}
