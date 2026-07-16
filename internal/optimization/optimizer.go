package optimization

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// Router is the narrow routing view the optimizer needs. *routing.Manager
// satisfies it. Route is used (not Select) so the optimizer sees the full ordered
// candidate list and can re-route with a downgraded model.
type Router interface {
	Route(ctx context.Context, rc routing.RoutingContext) (routing.RoutingDecision, error)
}

// Budget is the narrow budget view the optimizer needs. *budget.Manager satisfies
// it.
type Budget interface {
	Authorize(ctx context.Context, req budget.AuthorizeRequest) (budget.Decision, error)
	CommitUsage(ctx context.Context, scope budget.Scope, id, providerName, model string, usage provider.Usage, estimated float64) (float64, error)
	Budget(scope budget.Scope, id string) (budget.BudgetStatus, bool)
	CostModel() budget.CostModel
	Statistics() budget.Statistics
}

// LoadBalancer is the narrow balancer view the optimizer needs.
// *loadbalancer.Balancer satisfies it.
type LoadBalancer interface {
	Select(ctx context.Context, req loadbalancer.Request) (loadbalancer.Selection, error)
	Update(obs loadbalancer.Observation) error
	Statistics() loadbalancer.Statistics
}

// Optimizer coordinates the Budget Engine, Routing Engine, and Load Balancer into
// one pre-dispatch pipeline. Budget and LoadBalancer are optional; when nil, that
// stage is skipped.
type Optimizer struct {
	router  Router
	budget  Budget
	lb      LoadBalancer
	metrics ResourceMetrics
	log     logger.Logger

	requests    atomic.Int64
	downgrades  atomic.Int64
	rejects     atomic.Int64
	savingsNano atomic.Int64
}

// Option configures an Optimizer.
type Option func(*Optimizer)

// WithBudget enables the budget stage. A nil manager is ignored.
func WithBudget(b Budget) Option {
	return func(o *Optimizer) {
		if b != nil {
			o.budget = b
		}
	}
}

// WithLoadBalancer enables the instance-selection stage. A nil balancer is ignored.
func WithLoadBalancer(lb LoadBalancer) Option {
	return func(o *Optimizer) {
		if lb != nil {
			o.lb = lb
		}
	}
}

// WithMetrics injects a resource-metrics sink. A nil sink is ignored (default Nop).
func WithMetrics(m ResourceMetrics) Option {
	return func(o *Optimizer) {
		if m != nil {
			o.metrics = m
		}
	}
}

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) Option {
	return func(o *Optimizer) {
		if l != nil {
			o.log = l
		}
	}
}

// New constructs an Optimizer over a router, with optional budget and load
// balancer stages. It panics only on a nil router, a composition-root error.
func New(router Router, opts ...Option) *Optimizer {
	if router == nil {
		panic("optimization: Router must not be nil")
	}
	o := &Optimizer{
		router:  router,
		metrics: NopResourceMetrics{},
		log:     logger.Nop(),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Optimize runs the pipeline: route → budget authorize (reject / downgrade +
// re-route) → load balance. It never dispatches. A rejected request returns a
// non-nil Plan with Rejected set and a nil error, so the caller decides how to
// surface it; routing errors are returned as errors.
func (o *Optimizer) Optimize(ctx context.Context, req OptimizeRequest) (*Plan, error) {
	o.requests.Add(1)

	rc := o.routingContext(req)
	decision, err := o.router.Route(ctx, rc)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Provider:      decision.Selected.Provider,
		Model:         decision.Selected.Model,
		OriginalModel: decision.Selected.Model,
		Decision:      decision,
	}

	if o.budget != nil && req.BudgetID != "" {
		if err := o.applyBudget(ctx, req, plan); err != nil {
			return nil, err
		}
		if plan.Rejected {
			return plan, nil
		}
	}

	o.applyLoadBalancing(ctx, plan)
	o.log.Debug("optimized request",
		logger.String("provider", plan.Provider),
		logger.String("model", plan.Model),
		logger.String("instance", plan.InstanceID()),
		logger.Bool("downgraded", plan.Downgraded),
	)
	return plan, nil
}

// applyBudget runs the budget stage, mutating plan in place. It authorizes the
// routed model and, on a downgrade, re-runs routing with the cheaper model.
func (o *Optimizer) applyBudget(ctx context.Context, req OptimizeRequest, plan *Plan) error {
	scope := req.Scope
	if scope == "" {
		scope = budget.ScopeUser
	}
	inTok := req.InputTokens
	if inTok <= 0 {
		inTok = budget.EstimateTokens(renderPrompt(req.Chat.Messages))
	}
	outTok := req.ExpectedOutputTokens
	if outTok <= 0 {
		outTok = req.Chat.MaxTokens // may be 0 → the cost model applies its default
	}

	decision, err := o.budget.Authorize(ctx, budget.AuthorizeRequest{
		Scope:                scope,
		BudgetID:             req.BudgetID,
		Provider:             plan.Provider,
		Model:                plan.OriginalModel,
		InputTokens:          inTok,
		ExpectedOutputTokens: outTok,
	})
	if err != nil {
		return err
	}
	plan.Budget = &decision
	plan.EstimatedCost = decision.EstimatedCost
	plan.scope = scope
	plan.budgetID = req.BudgetID
	plan.hasBudget = true

	switch decision.Outcome {
	case budget.OutcomeReject:
		plan.Rejected = true
		o.rejects.Add(1)
		o.metrics.Reject(string(scope), req.BudgetID)
		o.emitBudgetUsage(scope, req.BudgetID)
		o.log.Info("request rejected by budget",
			logger.String("scope", string(scope)), logger.String("budget", req.BudgetID),
			logger.String("reason", decision.Reason))
		return nil

	case budget.OutcomeDowngrade:
		plan.Downgraded = true
		o.downgrades.Add(1)
		o.metrics.Downgrade(decision.OriginalModel, decision.Model)

		// Estimated savings = original-model estimate − downgraded estimate.
		origEst := o.budget.CostModel().Estimate(decision.OriginalModel, inTok, outTok)
		savings := origEst - decision.EstimatedCost
		if savings < 0 {
			savings = 0
		}
		plan.EstimatedSavings = savings
		o.savingsNano.Add(int64(savings * 1e9))
		o.metrics.EstimatedSavings(savings)

		// Re-run routing with the downgraded model so the best provider for the
		// cheaper model is chosen.
		rc := o.routingContext(req)
		rc.Model = decision.Model
		if newDecision, rerr := o.router.Route(ctx, rc); rerr == nil {
			plan.Decision = newDecision
			plan.Provider = newDecision.Selected.Provider
			plan.Model = newDecision.Selected.Model
		} else {
			// No provider advertises the downgraded model: keep the original
			// provider but serve the cheaper model (best effort).
			plan.Model = decision.Model
			o.log.Warn("re-routing downgraded model found no candidates; keeping original provider",
				logger.String("model", decision.Model), logger.Err(rerr))
		}
	}

	o.emitBudgetUsage(scope, req.BudgetID)
	return nil
}

// applyLoadBalancing runs the instance-selection stage, mutating plan in place. A
// provider with no registered instances leaves plan.LB nil (dispatch by name).
func (o *Optimizer) applyLoadBalancing(ctx context.Context, plan *Plan) {
	if o.lb == nil {
		return
	}
	sel, err := o.lb.Select(ctx, loadbalancer.Request{Provider: plan.Provider})
	if err != nil {
		if !errors.Is(err, loadbalancer.ErrNoInstances) {
			o.log.Warn("load balancer selection failed", logger.Err(err))
		}
		return
	}
	plan.LB = &sel
	o.metrics.LoadSelection(plan.Provider, sel.Instance.ID)
}

// Commit records the outcome of a dispatched request: it commits the actual cost
// to the budget (only on a successful, billable call) and feeds the observed
// latency back to the load balancer. It is a no-op for a rejected or nil plan.
func (o *Optimizer) Commit(ctx context.Context, plan *Plan, usage provider.Usage, latency time.Duration, success bool) error {
	if plan == nil || plan.Rejected {
		return nil
	}
	if o.budget != nil && plan.hasBudget && success {
		if _, err := o.budget.CommitUsage(ctx, plan.scope, plan.budgetID, plan.Provider, plan.Model, usage, plan.EstimatedCost); err != nil {
			return err
		}
		o.emitBudgetUsage(plan.scope, plan.budgetID)
	}
	if o.lb != nil && plan.InstanceID() != "" {
		_ = o.lb.Update(loadbalancer.Observation{InstanceID: plan.InstanceID(), Latency: latency, Success: success})
	}
	return nil
}

func (o *Optimizer) emitBudgetUsage(scope budget.Scope, id string) {
	if st, ok := o.budget.Budget(scope, id); ok {
		o.metrics.BudgetUsage(string(scope), id, st.CurrentUsage, st.Remaining)
	}
}

// routingContext builds the routing context for a request, carrying the
// correlation ID and any analysis-derived attributes.
func (o *Optimizer) routingContext(req OptimizeRequest) routing.RoutingContext {
	rc := routing.ChatContext(req.Chat)
	rc.RequestID = req.RequestID
	for k, v := range req.Attributes {
		if rc.Attributes == nil {
			rc.Attributes = make(map[string]any, len(req.Attributes))
		}
		if _, exists := rc.Attributes[k]; !exists {
			rc.Attributes[k] = v
		}
	}
	return rc
}

// renderPrompt produces a canonical single-string view of the conversation for
// token estimation. It is provider-agnostic and deterministic.
func renderPrompt(messages []provider.ChatMessage) string {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}
