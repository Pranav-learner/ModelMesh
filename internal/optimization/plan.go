package optimization

import (
	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// OptimizeRequest is the input to the optimization pipeline: the chat request to
// serve plus the budget identity to charge. Token counts are optional; when zero
// they are estimated from the prompt and the request's MaxTokens.
type OptimizeRequest struct {
	// Chat is the request being optimized.
	Chat provider.ChatRequest
	// Scope and BudgetID identify the budget to charge. An empty BudgetID skips
	// the budget stage entirely (routing + load balancing still run).
	Scope    budget.Scope
	BudgetID string
	// InputTokens / ExpectedOutputTokens override the estimated token counts used
	// for cost estimation (0 → estimate from the prompt / MaxTokens).
	InputTokens          int
	ExpectedOutputTokens int
	// RequestID correlates the decision with logs and traces.
	RequestID string
}

// Plan is the resolved optimization decision for one request: which provider,
// model, and instance should serve it, why, and what to commit afterward. On
// Rejected the request must not be dispatched.
type Plan struct {
	// Provider and Model are the resolved dispatch target (Model may be a downgrade).
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// OriginalModel is the model routing first selected, before any downgrade.
	OriginalModel string `json:"original_model"`

	// Decision is the (final) routing decision — re-run with the downgraded model
	// when a downgrade occurred.
	Decision routing.RoutingDecision `json:"decision"`
	// Budget is the budget verdict, or nil when the budget stage was skipped.
	Budget *budget.Decision `json:"budget,omitempty"`
	// LB is the load balancer's instance selection, or nil when no balancer is
	// wired or no instance was available (dispatch falls back to provider name).
	LB *loadbalancer.Selection `json:"load_balancer,omitempty"`

	// Downgraded and Rejected summarize the budget outcome.
	Downgraded bool `json:"downgraded"`
	Rejected   bool `json:"rejected"`

	// EstimatedCost is the estimated USD cost of the resolved request; savings is
	// the estimated USD avoided by a downgrade (0 otherwise).
	EstimatedCost    float64 `json:"estimated_cost"`
	EstimatedSavings float64 `json:"estimated_savings"`

	// commit context (unexported: set by Optimize, read by Commit).
	scope     budget.Scope
	budgetID  string
	hasBudget bool
}

// Allowed reports whether the request may be dispatched.
func (p *Plan) Allowed() bool { return p != nil && !p.Rejected }

// InstanceID returns the chosen instance ID, or "" when none was selected.
func (p *Plan) InstanceID() string {
	if p.LB != nil {
		return p.LB.Instance.ID
	}
	return ""
}

// Client returns the concrete provider client of the chosen instance, or nil when
// no instance was selected or the instance carries no client (dispatch by name).
func (p *Plan) Client() provider.LLMProvider {
	if p.LB != nil {
		return p.LB.Instance.Client
	}
	return nil
}
