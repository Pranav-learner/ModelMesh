package optimization_test

import (
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/optimization"
)

func TestExplainBudgetDecision(t *testing.T) {
	d := budget.Decision{Outcome: budget.OutcomeReject, Policy: "reject", Model: "gpt-4", EstimatedCost: 0.09, Remaining: 0.01, Reason: "over budget"}
	s := optimization.ExplainBudgetDecision(d)
	if !strings.Contains(s, "REJECT") || !strings.Contains(s, "gpt-4") || !strings.Contains(s, "over budget") {
		t.Errorf("explain budget missing detail: %s", s)
	}
}

func TestExplainDowngrade(t *testing.T) {
	none := optimization.ExplainDowngrade(&optimization.Plan{})
	if !strings.Contains(none, "no downgrade") {
		t.Errorf("expected no-downgrade note: %s", none)
	}
	p := &optimization.Plan{Downgraded: true, OriginalModel: "gpt-4", Model: "gpt-4o-mini", EstimatedSavings: 0.08, Provider: "openai"}
	s := optimization.ExplainDowngrade(p)
	if !strings.Contains(s, "gpt-4 → gpt-4o-mini") || !strings.Contains(s, "openai") {
		t.Errorf("explain downgrade missing detail: %s", s)
	}
}

func TestExplainLoadBalancingAndPlan(t *testing.T) {
	sel := loadbalancer.Selection{
		Strategy: "round_robin",
		Instance: loadbalancer.Instance{ID: "openai-a", Provider: "openai", Region: "us-east-1"},
		Stats:    loadbalancer.InstanceStats{ID: "openai-a", RequestCount: 3},
	}
	if s := optimization.ExplainLoadBalancing(sel); !strings.Contains(s, "openai-a") || !strings.Contains(s, "round_robin") {
		t.Errorf("explain LB missing detail: %s", s)
	}

	bd := budget.Decision{Outcome: budget.OutcomeDowngrade, Policy: "downgrade", Model: "gpt-4o-mini"}
	plan := &optimization.Plan{Provider: "openai", Model: "gpt-4o-mini", OriginalModel: "gpt-4", Downgraded: true, EstimatedSavings: 0.08, Budget: &bd, LB: &sel}
	full := optimization.ExplainPlan(plan)
	for _, want := range []string{"provider=openai", "instance=openai-a", "downgraded from gpt-4", "budget", "load balancer"} {
		if !strings.Contains(full, want) {
			t.Errorf("ExplainPlan missing %q:\n%s", want, full)
		}
	}
	if optimization.ExplainPlan(nil) != "no plan" {
		t.Errorf("nil plan should be 'no plan'")
	}
	rejected := optimization.ExplainPlan(&optimization.Plan{Provider: "openai", Model: "gpt-4", Rejected: true})
	if !strings.Contains(rejected, "REJECTED") {
		t.Errorf("rejected plan should show REJECTED: %s", rejected)
	}
}

func TestResourceUsage_Snapshot(t *testing.T) {
	router := newRouter(t, testProvider("openai", "gpt-4", "gpt-4o-mini"))
	bm := newBudget(t, budget.PolicyDowngrade, "gpt-4o-mini")
	_ = bm.SetBudget(budget.UserBudget("u1", 1.0))
	lb := loadbalancer.New(loadbalancer.Config{Strategy: loadbalancer.StrategyRoundRobin}, loadbalancer.NewRoundRobin())
	_ = lb.Register(loadbalancer.Instance{ID: "openai-a", Provider: "openai"})
	o := optimization.New(router, optimization.WithBudget(bm), optimization.WithLoadBalancer(lb))
	ctx := context.Background()

	// Drive one normal request.
	plan, _ := o.Optimize(ctx, optimization.OptimizeRequest{Chat: chatReq("gpt-4", "hi"), Scope: budget.ScopeUser, BudgetID: "u1", InputTokens: 100, ExpectedOutputTokens: 100})
	_ = o.Commit(ctx, plan, providerUsage(), 0, true)

	u := o.ResourceUsage()
	if u.Requests != 1 {
		t.Errorf("requests = %d, want 1", u.Requests)
	}
	if u.BudgetPolicy != "downgrade" {
		t.Errorf("budget policy = %q", u.BudgetPolicy)
	}
	if u.Strategy != "round_robin" {
		t.Errorf("lb strategy = %q", u.Strategy)
	}
	if len(u.Budgets) != 1 || len(u.Instances) != 1 {
		t.Errorf("snapshot budgets=%d instances=%d, want 1/1", len(u.Budgets), len(u.Instances))
	}
	if u.TotalSpentUSD <= 0 {
		t.Errorf("total spent should be > 0")
	}
}
