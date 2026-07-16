package budget_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/logger"
)

func TestManager_OptionsWiring(t *testing.T) {
	logs := &bytes.Buffer{}
	log := logger.NewWithWriter(logs, logger.LevelDebug)

	cfg := budget.Config{Policy: budget.PolicyReject, Pricing: pricing()}
	m, err := budget.NewManager(cfg,
		budget.WithLogger(log),
		budget.WithPolicy(budget.DowngradePolicy{}), // override policy directly
		budget.WithCostModel(budget.NewPricingModel(pricing(), 500, 200)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if m.Policy() != budget.PolicyDowngrade {
		t.Errorf("policy override not applied: %s", m.Policy())
	}

	_ = m.SetBudget(budget.UserBudget("u1", 100))
	if _, err := m.Authorize(context.Background(), budget.AuthorizeRequest{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "budget decision") {
		t.Errorf("expected decision log, got: %s", logs.String())
	}
}

func TestManager_SetBudgetUpdatesLimitPreservesUsage(t *testing.T) {
	m, _ := budget.NewManager(budget.Config{Policy: budget.PolicyReject, Pricing: pricing()})
	_ = m.SetBudget(budget.UserBudget("u1", 10))
	_ = m.Commit(context.Background(), budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u1", Model: "mini", ActualCost: 4})

	// Tighten the limit; the day's spend must persist.
	_ = m.SetBudget(budget.UserBudget("u1", 5))
	st, _ := m.Budget(budget.ScopeUser, "u1")
	if st.DailyLimit != 5 || st.CurrentUsage != 4 || st.Remaining != 1 {
		t.Errorf("after limit change: limit=%v usage=%v remaining=%v, want 5/4/1", st.DailyLimit, st.CurrentUsage, st.Remaining)
	}
}

func TestManager_StatisticsSortedAcrossScopes(t *testing.T) {
	m, _ := budget.NewManager(budget.Config{Policy: budget.PolicyReject, Pricing: pricing()})
	ctx := context.Background()
	_ = m.SetBudget(budget.TeamBudget("t-z", 100))
	_ = m.SetBudget(budget.UserBudget("u-b", 10))
	_ = m.SetBudget(budget.UserBudget("u-a", 10))
	_ = m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeUser, BudgetID: "u-a", Model: "mini", ActualCost: 1})
	_ = m.Commit(ctx, budget.UsageRecord{Scope: budget.ScopeTeam, BudgetID: "t-z", Model: "mini", ActualCost: 2})

	stats := m.Statistics()
	if stats.TotalBudgets != 3 {
		t.Errorf("total budgets = %d, want 3", stats.TotalBudgets)
	}
	if stats.TotalSpent != 3 {
		t.Errorf("total spent = %v, want 3", stats.TotalSpent)
	}
	// Sorted by scope then ID: team:t-z, user:u-a, user:u-b.
	order := []string{"t-z", "u-a", "u-b"}
	for i, want := range order {
		if stats.Budgets[i].ID != want {
			t.Errorf("budgets[%d] = %s, want %s (order %v)", i, stats.Budgets[i].ID, want, order)
		}
	}
}
