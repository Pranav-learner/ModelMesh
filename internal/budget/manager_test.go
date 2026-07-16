package budget_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func pricing() budget.PricingConfig {
	return budget.PricingConfig{
		Models: map[string]budget.ModelPricing{
			"expensive": {InputPer1K: 0.1, OutputPer1K: 0.2},
			"mini":      {InputPer1K: 0.0005, OutputPer1K: 0.0015},
		},
		Default: budget.ModelPricing{InputPer1K: 0.001, OutputPer1K: 0.002},
	}
}

// mutableClock is a test clock advanceable across goroutines.
type mutableClock struct{ ns atomic.Int64 }

func newClock(t time.Time) *mutableClock {
	c := &mutableClock{}
	c.ns.Store(t.UnixNano())
	return c
}
func (c *mutableClock) now() time.Time          { return time.Unix(0, c.ns.Load()).UTC() }
func (c *mutableClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func newManager(t *testing.T, cfg budget.Config, clk func() time.Time) *budget.Manager {
	t.Helper()
	cfg.Pricing = pricing()
	m, err := budget.NewManager(cfg, budget.WithClock(clk))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return m
}

// --- Budget Exhaustion ---

func TestManager_BudgetExhaustion(t *testing.T) {
	clk := newClock(time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC))
	m := newManager(t, budget.Config{Policy: budget.PolicyReject}, clk.now)
	if err := m.SetBudget(budget.UserBudget("u1", 1.0)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := budget.AuthorizeRequest{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini", Provider: "openai", InputTokens: 1000, ExpectedOutputTokens: 500}

	// Initially affordable.
	if d, _ := m.Authorize(ctx, req); d.Outcome != budget.OutcomeAllow {
		t.Fatalf("initial authorize = %s, want allow", d.Outcome)
	}

	// Spend the whole budget.
	if err := m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini", ActualCost: 1.0}); err != nil {
		t.Fatal(err)
	}

	// Now exhausted → reject.
	d, _ := m.Authorize(ctx, req)
	if d.Outcome != budget.OutcomeReject || d.Allowed() {
		t.Errorf("exhausted authorize = %s (allowed=%v), want reject", d.Outcome, d.Allowed())
	}
	if st, _ := m.Budget(budget.ScopeUser, "u1"); st.Remaining != 0 {
		t.Errorf("remaining = %v, want 0", st.Remaining)
	}
}

// --- Downgrade ---

func TestManager_Downgrade(t *testing.T) {
	clk := newClock(time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC))
	m := newManager(t, budget.Config{Policy: budget.PolicyDowngrade, DefaultModel: "mini", DowngradeThreshold: 0.15}, clk.now)
	_ = m.SetBudget(budget.UserBudget("u1", 1.0))
	ctx := context.Background()

	// Consume most of the budget, leaving 0.2.
	_ = m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "expensive", ActualCost: 0.8})

	// expensive: 1K*0.1 + 1K*0.2 = 0.3 > 0.2 remaining → downgrade to mini.
	req := budget.AuthorizeRequest{Scope: budget.ScopeUser, BudgetID: "u1", Model: "expensive", InputTokens: 1000, ExpectedOutputTokens: 1000}
	d, _ := m.Authorize(ctx, req)
	if d.Outcome != budget.OutcomeDowngrade {
		t.Fatalf("authorize = %s, want downgrade", d.Outcome)
	}
	if d.Model != "mini" || d.OriginalModel != "expensive" {
		t.Errorf("downgraded to %s (from %s), want mini", d.Model, d.OriginalModel)
	}
	if d.EstimatedCost >= 0.3 {
		t.Errorf("downgraded estimate %v should be far below the expensive 0.3", d.EstimatedCost)
	}
}

// --- Cost Estimation (surfaced through Authorize) ---

func TestManager_AuthorizeReportsEstimate(t *testing.T) {
	m := newManager(t, budget.Config{Policy: budget.PolicyReject}, nil)
	_ = m.SetBudget(budget.UserBudget("u1", 100))

	req := budget.AuthorizeRequest{Scope: budget.ScopeUser, BudgetID: "u1", Model: "expensive", InputTokens: 1000, ExpectedOutputTokens: 500}
	d, _ := m.Authorize(context.Background(), req)
	// 1K*0.1 + 0.5K*0.2 = 0.1 + 0.1 = 0.2
	want := 0.2
	if d.EstimatedCost < want-1e-9 || d.EstimatedCost > want+1e-9 {
		t.Errorf("estimate = %v, want %v", d.EstimatedCost, want)
	}
	// Cross-check against the exposed cost model.
	if got := m.CostModel().Estimate("expensive", 1000, 500); got != d.EstimatedCost {
		t.Errorf("decision estimate %v != cost model %v", d.EstimatedCost, got)
	}
}

// --- Concurrent Updates ---

func TestManager_ConcurrentCommits(t *testing.T) {
	clk := newClock(time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC))
	m := newManager(t, budget.Config{Policy: budget.PolicyReject, LedgerSize: 100000}, clk.now)
	_ = m.SetBudget(budget.TeamBudget("team-1", 1_000_000))
	ctx := context.Background()

	const workers, per = 16, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeTeam, BudgetID: "team-1", Model: "mini", Tokens: 10, ActualCost: 0.01}); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	want := float64(workers*per) * 0.01 // 16.0
	st, _ := m.Budget(budget.ScopeTeam, "team-1")
	if st.CurrentUsage < want-1e-6 || st.CurrentUsage > want+1e-6 {
		t.Errorf("usage = %v, want %v (lost updates?)", st.CurrentUsage, want)
	}
	if n := len(m.Usage(budget.ScopeTeam, "team-1")); n != workers*per {
		t.Errorf("ledger has %d records, want %d", n, workers*per)
	}
}

// --- Usage Tracking ---

func TestManager_UsageTracking(t *testing.T) {
	clk := newClock(time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC))
	m := newManager(t, budget.Config{Policy: budget.PolicyReject}, clk.now)
	ctx := context.Background()

	actual, err := m.CommitUsage(ctx, budget.ScopeUser, "u1", "openai", "expensive",
		provider.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	// 1K*0.1 + 0.5K*0.2 = 0.2
	if actual < 0.2-1e-9 || actual > 0.2+1e-9 {
		t.Errorf("actual cost = %v, want 0.2", actual)
	}

	records := m.Usage(budget.ScopeUser, "u1")
	if len(records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(records))
	}
	r := records[0]
	if r.Provider != "openai" || r.Model != "expensive" || r.Tokens != 1500 {
		t.Errorf("record metadata wrong: %+v", r)
	}
	if r.Timestamp.IsZero() {
		t.Errorf("record timestamp not stamped")
	}
	if r.ActualCost != actual || r.EstimatedCost != 0.2 {
		t.Errorf("record costs: actual=%v estimated=%v", r.ActualCost, r.EstimatedCost)
	}

	stats := m.Statistics()
	if stats.TotalSpent < 0.2-1e-9 || stats.TotalSpent > 0.2+1e-9 {
		t.Errorf("total spent = %v, want 0.2", stats.TotalSpent)
	}
}

func TestManager_LedgerBounded(t *testing.T) {
	m := newManager(t, budget.Config{Policy: budget.PolicyReject, LedgerSize: 5}, nil)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		_ = m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini", ActualCost: 0.001})
	}
	if n := len(m.Usage(budget.ScopeUser, "u1")); n != 5 {
		t.Errorf("bounded ledger len = %d, want 5", n)
	}
}

// --- Auto-provisioning, windows, validation ---

func TestManager_AutoProvisionFromDefaults(t *testing.T) {
	m := newManager(t, budget.Config{Policy: budget.PolicyReject, DefaultUserDailyLimit: 3, DefaultTeamDailyLimit: 30}, nil)
	if st, _ := m.Budget(budget.ScopeUser, "new-user"); st.DailyLimit != 3 {
		t.Errorf("auto user limit = %v, want 3", st.DailyLimit)
	}
	if st, _ := m.Budget(budget.ScopeTeam, "new-team"); st.DailyLimit != 30 {
		t.Errorf("auto team limit = %v, want 30", st.DailyLimit)
	}
}

func TestManager_WindowResetsUsage(t *testing.T) {
	clk := newClock(time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC))
	m := newManager(t, budget.Config{Policy: budget.PolicyReject}, clk.now)
	_ = m.SetBudget(budget.UserBudget("u1", 10))
	ctx := context.Background()
	_ = m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini", ActualCost: 6})

	if st, _ := m.Budget(budget.ScopeUser, "u1"); st.CurrentUsage != 6 {
		t.Fatalf("usage before reset = %v, want 6", st.CurrentUsage)
	}
	clk.advance(25 * time.Hour) // cross the daily window boundary
	if st, _ := m.Budget(budget.ScopeUser, "u1"); st.CurrentUsage != 0 {
		t.Errorf("usage after window = %v, want 0 (reset)", st.CurrentUsage)
	}
}

func TestManager_InvalidScopeAndBudget(t *testing.T) {
	m := newManager(t, budget.DefaultConfig(), nil)
	if _, err := m.Authorize(context.Background(), budget.AuthorizeRequest{Scope: "org", BudgetID: "x", Model: "mini"}); !errors.Is(err, budget.ErrInvalidScope) {
		t.Errorf("bad scope authorize = %v, want ErrInvalidScope", err)
	}
	if err := m.Commit(context.Background(), budget.UsageRecord{Scope: "org", BudgetID: "x"}); !errors.Is(err, budget.ErrInvalidScope) {
		t.Errorf("bad scope commit = %v, want ErrInvalidScope", err)
	}
	if err := m.SetBudget(budget.Budget{ID: "", Scope: budget.ScopeUser}); !errors.Is(err, budget.ErrInvalidBudget) {
		t.Errorf("invalid budget = %v, want ErrInvalidBudget", err)
	}
	if _, ok := m.Budget("org", "x"); ok {
		t.Errorf("invalid scope lookup should return ok=false")
	}
}

func TestNewManager_ConfigErrors(t *testing.T) {
	if _, err := budget.NewManager(budget.Config{Policy: budget.PolicyDowngrade}); !errors.Is(err, budget.ErrInvalidConfig) {
		t.Errorf("downgrade without default_model = %v, want ErrInvalidConfig", err)
	}
	if _, err := budget.NewManager(budget.Config{Policy: "nope", DefaultModel: "m"}); !errors.Is(err, budget.ErrUnknownPolicy) {
		t.Errorf("unknown policy = %v, want ErrUnknownPolicy", err)
	}
}
