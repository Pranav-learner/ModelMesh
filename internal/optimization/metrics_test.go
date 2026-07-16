package optimization_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/optimization"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func providerUsage() provider.Usage {
	return provider.Usage{PromptTokens: 100, CompletionTokens: 100, TotalTokens: 200}
}

// recordingMetrics captures resource-metric calls.
type recordingMetrics struct {
	mu         sync.Mutex
	budgetHits int
	downgrades int
	rejects    int
	selections int
	savings    float64
}

func (r *recordingMetrics) BudgetUsage(_, _ string, _, _ float64) { r.lock(func() { r.budgetHits++ }) }
func (r *recordingMetrics) Downgrade(_, _ string)                 { r.lock(func() { r.downgrades++ }) }
func (r *recordingMetrics) Reject(_, _ string)                    { r.lock(func() { r.rejects++ }) }
func (r *recordingMetrics) LoadSelection(_, _ string)             { r.lock(func() { r.selections++ }) }
func (r *recordingMetrics) EstimatedSavings(v float64)            { r.lock(func() { r.savings += v }) }
func (r *recordingMetrics) lock(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

func TestMetrics_DowngradeAndSavingsRecorded(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4", "gpt-4o-mini"))
	bm := newBudget(t, budget.PolicyDowngrade, "gpt-4o-mini")
	_ = bm.SetBudget(budget.UserBudget("u1", 1.0))
	rec := &recordingMetrics{}
	o := optimization.New(router, optimization.WithBudget(bm), optimization.WithMetrics(rec))
	ctx := context.Background()
	_ = bm.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.95})

	plan, _ := o.Optimize(ctx, optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 1000, ExpectedOutputTokens: 1000})
	if !plan.Downgraded {
		t.Fatalf("expected downgrade")
	}
	if rec.downgrades != 1 || rec.savings <= 0 || rec.budgetHits == 0 {
		t.Errorf("metrics: downgrades=%d savings=%v budgetHits=%d", rec.downgrades, rec.savings, rec.budgetHits)
	}
}

func TestMetrics_RejectRecorded(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	bm := newBudget(t, budget.PolicyReject, "")
	_ = bm.SetBudget(budget.UserBudget("u1", 0.001))
	rec := &recordingMetrics{}
	o := optimization.New(router, optimization.WithBudget(bm), optimization.WithMetrics(rec))
	ctx := context.Background()
	_ = bm.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "gpt-4", ActualCost: 0.001})

	plan, _ := o.Optimize(ctx, optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 1000, ExpectedOutputTokens: 1000})
	if !plan.Rejected {
		t.Fatalf("expected reject")
	}
	if rec.rejects != 1 {
		t.Errorf("rejects = %d, want 1", rec.rejects)
	}
}

func TestOptimizer_LoggerWiring(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	logs := &bytes.Buffer{}
	o := optimization.New(router, optimization.WithLogger(logger.NewWithWriter(logs, logger.LevelDebug)))
	if _, err := o.Optimize(context.Background(), optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "optimized request") {
		t.Errorf("expected optimize log, got: %s", logs.String())
	}
}

func TestOptimize_NoBudgetNoLBSkipsStages(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4"))
	o := optimization.New(router) // router only
	plan, err := o.Optimize(context.Background(), optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Budget != nil || plan.LB != nil {
		t.Errorf("stages should be skipped without collaborators")
	}
	// Commit is a safe no-op without collaborators.
	if err := o.Commit(context.Background(), plan, providerUsage(), 0, true); err != nil {
		t.Errorf("commit no-op: %v", err)
	}
}
