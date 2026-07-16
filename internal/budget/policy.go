package budget

import "fmt"

// PolicyInput is everything a Policy needs to reach a verdict: the request, the
// budget's live status, the estimated cost of the requested model, the cost model
// (so a policy can price alternatives), and the config knobs.
type PolicyInput struct {
	Request   AuthorizeRequest
	Status    BudgetStatus
	Estimate  float64
	CostModel CostModel
	Config    Config
}

// Policy is the pluggable enforcement strategy applied when deciding whether a
// request fits its budget. Reject and Downgrade are implemented; Queue, Notify,
// and Approval are reserved. A policy is pure: it reads the input and returns a
// Decision, mutating nothing — the Manager owns all state.
type Policy interface {
	// Name returns the stable policy identifier (e.g. "reject").
	Name() string
	// Decide returns the verdict for the request described by in.
	Decide(in PolicyInput) Decision
}

// baseDecision seeds a Decision with the fields common to every verdict.
func baseDecision(in PolicyInput, policy string) Decision {
	return Decision{
		Model:         in.Request.Model,
		OriginalModel: in.Request.Model,
		EstimatedCost: in.Estimate,
		Remaining:     in.Status.Remaining,
		Policy:        policy,
	}
}

// RejectPolicy allows a request that fits the remaining budget and rejects one
// that does not. It is the safe default.
type RejectPolicy struct{}

// Name returns the policy identifier.
func (RejectPolicy) Name() string { return PolicyReject }

// Decide allows if the estimate fits, else rejects.
func (RejectPolicy) Decide(in PolicyInput) Decision {
	d := baseDecision(in, PolicyReject)
	if in.Estimate <= in.Status.Remaining {
		d.Outcome = OutcomeAllow
		d.Reason = "request fits remaining budget"
		return d
	}
	d.Outcome = OutcomeReject
	d.Reason = fmt.Sprintf("estimated cost %.6f exceeds remaining budget %.6f", in.Estimate, in.Status.Remaining)
	return d
}

// DowngradePolicy switches to the configured default (cheaper) model when the
// requested model does not fit, or proactively when remaining budget has fallen
// below the downgrade threshold. If even the cheaper model does not fit, it
// rejects.
type DowngradePolicy struct{}

// Name returns the policy identifier.
func (DowngradePolicy) Name() string { return PolicyDowngrade }

// Decide applies the downgrade logic described on the type.
func (DowngradePolicy) Decide(in PolicyInput) Decision {
	d := baseDecision(in, PolicyDowngrade)
	remaining := in.Status.Remaining
	fits := in.Estimate <= remaining
	lowBudget := remaining < in.Config.DowngradeThreshold*in.Status.DailyLimit

	// Plenty of budget and the request fits: allow as-is.
	if fits && !lowBudget {
		d.Outcome = OutcomeAllow
		d.Reason = "request fits remaining budget"
		return d
	}

	// Try to downgrade to the cheaper default model.
	def := in.Config.DefaultModel
	if def != "" && def != in.Request.Model {
		dcost := in.CostModel.Estimate(def, in.Request.InputTokens, in.Request.ExpectedOutputTokens)
		if dcost <= remaining {
			d.Outcome = OutcomeDowngrade
			d.Model = def
			d.EstimatedCost = dcost
			if fits {
				d.Reason = fmt.Sprintf("remaining budget %.6f below threshold; downgraded %s→%s to conserve", remaining, in.Request.Model, def)
			} else {
				d.Reason = fmt.Sprintf("requested model exceeds budget; downgraded %s→%s (cost %.6f)", in.Request.Model, def, dcost)
			}
			return d
		}
	}

	// Downgrade unavailable or insufficient. Allow if the original still fits,
	// otherwise reject.
	if fits {
		d.Outcome = OutcomeAllow
		d.Reason = "no cheaper model available; original fits remaining budget"
		return d
	}
	d.Outcome = OutcomeReject
	d.Reason = fmt.Sprintf("estimated cost %.6f exceeds remaining budget %.6f and no affordable downgrade", in.Estimate, remaining)
	return d
}

// PolicyBuilder constructs a Policy from configuration.
type PolicyBuilder func(cfg Config) (Policy, error)

// PolicyRegistry maps policy names to builders — the extension seam for new
// enforcement strategies.
type PolicyRegistry struct {
	builders map[string]PolicyBuilder
}

// NewPolicyRegistry returns an empty policy registry.
func NewPolicyRegistry() *PolicyRegistry {
	return &PolicyRegistry{builders: make(map[string]PolicyBuilder)}
}

// Register adds a builder under name, overwriting any existing entry.
func (r *PolicyRegistry) Register(name string, b PolicyBuilder) { r.builders[name] = b }

// Build instantiates the named policy, distinguishing reserved-but-unimplemented
// names from unknown ones.
func (r *PolicyRegistry) Build(name string, cfg Config) (Policy, error) {
	if b, ok := r.builders[name]; ok {
		return b(cfg)
	}
	if isReserved(name) {
		return nil, fmt.Errorf("%w: %q", ErrPolicyNotImplemented, name)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownPolicy, name)
}

// DefaultPolicyRegistry returns a registry with the implemented policies.
func DefaultPolicyRegistry() *PolicyRegistry {
	r := NewPolicyRegistry()
	r.Register(PolicyReject, func(Config) (Policy, error) { return RejectPolicy{}, nil })
	r.Register(PolicyDowngrade, func(Config) (Policy, error) { return DowngradePolicy{}, nil })
	return r
}
