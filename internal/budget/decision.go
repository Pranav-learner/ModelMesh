package budget

import "time"

// AuthorizeRequest describes a candidate request to be pre-authorized: whose
// budget it draws on, and what it will cost. Token counts are optional; when zero
// the cost model's configured defaults are used.
type AuthorizeRequest struct {
	// Scope and BudgetID identify the budget to charge (e.g. user "u-42").
	Scope    Scope
	BudgetID string
	// Provider and Model are the routed target (Provider is recorded for
	// accounting; pricing is keyed by Model).
	Provider string
	Model    string
	// InputTokens is the estimated prompt token count (0 → config default).
	InputTokens int
	// ExpectedOutputTokens is the anticipated completion length (0 → config default).
	ExpectedOutputTokens int
}

// Outcome is the verdict of a budget authorization.
type Outcome string

const (
	// OutcomeAllow permits the request as-is.
	OutcomeAllow Outcome = "allow"
	// OutcomeDowngrade permits the request but on a cheaper model (Decision.Model).
	OutcomeDowngrade Outcome = "downgrade"
	// OutcomeReject denies the request; it must not be dispatched.
	OutcomeReject Outcome = "reject"
)

// Decision is the result of Authorize. It is a pure verdict: nothing has been
// spent. On OutcomeAllow/OutcomeDowngrade the caller dispatches using Model, then
// Commits the actual cost; on OutcomeReject it must not dispatch.
type Decision struct {
	Outcome Outcome `json:"outcome"`
	// Model is the model the caller should use — the original for Allow/Reject, or
	// the cheaper substitute for Downgrade.
	Model string `json:"model"`
	// OriginalModel is the model requested before any downgrade.
	OriginalModel string `json:"original_model"`
	// EstimatedCost is the estimated cost (USD) of the request as decided (i.e. of
	// Model).
	EstimatedCost float64 `json:"estimated_cost"`
	// Remaining is the budget remaining at decision time.
	Remaining float64 `json:"remaining"`
	// Policy is the enforcement policy that produced the verdict.
	Policy string `json:"policy"`
	// Reason is a short human-readable explanation.
	Reason string `json:"reason"`
}

// Allowed reports whether the request may be dispatched (allow or downgrade).
func (d Decision) Allowed() bool { return d.Outcome != OutcomeReject }

// Downgraded reports whether the model was changed to conserve budget.
func (d Decision) Downgraded() bool { return d.Outcome == OutcomeDowngrade }

// UsageRecord is one accounting entry: what a completed request actually cost and
// the metadata to attribute it. It is the input to Commit and the unit returned
// by Usage.
type UsageRecord struct {
	Scope         Scope     `json:"scope"`
	BudgetID      string    `json:"budget_id"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	Tokens        int       `json:"tokens"`
	EstimatedCost float64   `json:"estimated_cost"`
	ActualCost    float64   `json:"actual_cost"`
	Timestamp     time.Time `json:"timestamp"`
}
