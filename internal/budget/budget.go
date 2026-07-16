package budget

import "time"

// Scope identifies the kind of budget a spend is accounted against. The engine
// supports per-user and per-team budgets; both share one model, distinguished by
// scope, so a new scope (per-org, per-key, ...) is a constant, not a new type.
type Scope string

const (
	ScopeUser Scope = "user"
	ScopeTeam Scope = "team"
)

// valid reports whether s is a supported scope.
func (s Scope) valid() bool { return s == ScopeUser || s == ScopeTeam }

// Budget is the configuration of a single spending limit: who it applies to and
// the daily ceiling. Runtime state (current usage, reset time) is maintained by
// the Manager and reported via BudgetStatus, so a Budget value is a stable
// descriptor safe to store and pass around.
type Budget struct {
	// ID is the user or team identifier the budget applies to. Required.
	ID string `json:"id"`
	// Scope is user or team. Required.
	Scope Scope `json:"scope"`
	// DailyLimit is the maximum spend (USD) allowed within a 24h window. Zero is a
	// valid "no spend allowed" limit; negative is invalid.
	DailyLimit float64 `json:"daily_limit"`
}

// UserBudget constructs a per-user Budget with the given daily limit (USD).
func UserBudget(id string, dailyLimit float64) Budget {
	return Budget{ID: id, Scope: ScopeUser, DailyLimit: dailyLimit}
}

// TeamBudget constructs a per-team Budget with the given daily limit (USD).
func TeamBudget(id string, dailyLimit float64) Budget {
	return Budget{ID: id, Scope: ScopeTeam, DailyLimit: dailyLimit}
}

func (b Budget) valid() bool { return b.ID != "" && b.Scope.valid() && b.DailyLimit >= 0 }

// BudgetStatus is a point-in-time snapshot of a budget's limit and live usage.
type BudgetStatus struct {
	ID           string    `json:"id"`
	Scope        Scope     `json:"scope"`
	DailyLimit   float64   `json:"daily_limit"`
	CurrentUsage float64   `json:"current_usage"`
	Remaining    float64   `json:"remaining"`
	ResetAt      time.Time `json:"reset_at"`
}

// managedBudget is the Manager's mutable runtime state for one budget, guarded by
// the Manager's mutex.
type managedBudget struct {
	desc    Budget
	usage   float64
	resetAt time.Time
}

// window is the budget reset period. Daily budgets reset every 24h; the boundary
// is aligned to the clock's day so resets are predictable.
const window = 24 * time.Hour

// rollover resets usage to zero if the window has elapsed, advancing resetAt past
// now. It is called on every access under lock, so a budget is always current.
func (m *managedBudget) rollover(now time.Time) {
	if !now.Before(m.resetAt) {
		m.usage = 0
		m.resetAt = nextReset(now)
	}
}

// nextReset returns the next window boundary strictly after now, aligned to the
// start of the next UTC day.
func nextReset(now time.Time) time.Time {
	day := now.UTC().Truncate(window)
	reset := day.Add(window)
	if !reset.After(now) {
		reset = reset.Add(window)
	}
	return reset
}

func (m *managedBudget) status() BudgetStatus {
	return BudgetStatus{
		ID:           m.desc.ID,
		Scope:        m.desc.Scope,
		DailyLimit:   m.desc.DailyLimit,
		CurrentUsage: m.usage,
		Remaining:    m.remaining(),
		ResetAt:      m.resetAt,
	}
}

// remaining is the un-spent budget, floored at zero.
func (m *managedBudget) remaining() float64 {
	r := m.desc.DailyLimit - m.usage
	if r < 0 {
		return 0
	}
	return r
}
