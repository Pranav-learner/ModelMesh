package optimization

import (
	"fmt"
	"strings"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
)

// ExplainBudgetDecision renders a budget verdict as an operator-readable line.
func ExplainBudgetDecision(d budget.Decision) string {
	return fmt.Sprintf("budget [%s]: %s — model=%s estimate=$%.6f remaining=$%.6f (%s)",
		d.Policy, strings.ToUpper(string(d.Outcome)), d.Model, d.EstimatedCost, d.Remaining, d.Reason)
}

// ExplainDowngrade renders a plan's downgrade, or a note that none occurred.
func ExplainDowngrade(p *Plan) string {
	if p == nil || !p.Downgraded {
		return "no downgrade: request served on the originally routed model"
	}
	return fmt.Sprintf("downgraded %s → %s to stay within budget; estimated savings $%.6f (now routed to provider %q)",
		p.OriginalModel, p.Model, p.EstimatedSavings, p.Provider)
}

// ExplainLoadBalancing renders a load balancer instance selection.
func ExplainLoadBalancing(sel loadbalancer.Selection) string {
	i := sel.Instance
	return fmt.Sprintf("load balancer [%s]: provider=%s instance=%s region=%s (avg latency %s over %d requests)",
		sel.Strategy, i.Provider, i.ID, i.Region, sel.Stats.AverageLatency, sel.Stats.RequestCount)
}

// ExplainPlan renders the full optimization decision for one request.
func ExplainPlan(p *Plan) string {
	if p == nil {
		return "no plan"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "provider=%s model=%s", p.Provider, p.Model)
	if p.InstanceID() != "" {
		fmt.Fprintf(&b, " instance=%s", p.InstanceID())
	}
	if p.Rejected {
		b.WriteString(" [REJECTED]")
	} else if p.Downgraded {
		fmt.Fprintf(&b, " [downgraded from %s, saved ~$%.6f]", p.OriginalModel, p.EstimatedSavings)
	}
	if p.Budget != nil {
		fmt.Fprintf(&b, "\n  %s", ExplainBudgetDecision(*p.Budget))
	}
	if p.LB != nil {
		fmt.Fprintf(&b, "\n  %s", ExplainLoadBalancing(*p.LB))
	}
	return b.String()
}

// ResourceUsage is a combined snapshot of the optimization layer: aggregate
// optimizer counters plus the live budget and load balancer read models. It backs
// the "display resource usage" diagnostic.
type ResourceUsage struct {
	Requests            int64                        `json:"requests"`
	Downgrades          int64                        `json:"downgrades"`
	Rejects             int64                        `json:"rejects"`
	EstimatedSavingsUSD float64                      `json:"estimated_savings_usd"`
	BudgetPolicy        string                       `json:"budget_policy,omitempty"`
	Budgets             []budget.BudgetStatus        `json:"budgets,omitempty"`
	TotalSpentUSD       float64                      `json:"total_spent_usd"`
	Strategy            string                       `json:"lb_strategy,omitempty"`
	Instances           []loadbalancer.InstanceStats `json:"instances,omitempty"`
}

// ResourceUsage returns the combined optimization snapshot.
func (o *Optimizer) ResourceUsage() ResourceUsage {
	u := ResourceUsage{
		Requests:            o.requests.Load(),
		Downgrades:          o.downgrades.Load(),
		Rejects:             o.rejects.Load(),
		EstimatedSavingsUSD: float64(o.savingsNano.Load()) / 1e9,
	}
	if o.budget != nil {
		bs := o.budget.Statistics()
		u.BudgetPolicy = bs.Policy
		u.Budgets = bs.Budgets
		u.TotalSpentUSD = bs.TotalSpent
	}
	if o.lb != nil {
		ls := o.lb.Statistics()
		u.Strategy = ls.Strategy
		u.Instances = ls.Instances
	}
	return u
}
