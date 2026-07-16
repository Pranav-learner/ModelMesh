package budget

import (
	"context"
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// Manager is the budget engine façade. It owns budget lookup, tracking, updates,
// and validation; the cost model and enforcement policy are collaborators.
//
// State (per-scope daily counters + a bounded usage ledger) lives here behind a
// single mutex. Authorize is a pure read; Commit is the only mutation and is safe
// under concurrency. A distributed deployment would back the counters with Redis
// (see the Budget Engine design); the in-memory store here is operational, not an
// accounting ledger.
type Manager struct {
	cfg    Config
	cost   CostModel
	policy Policy
	log    logger.Logger
	clock  func() time.Time

	mu      sync.Mutex
	budgets map[string]*managedBudget
	ledger  map[string][]UsageRecord
}

// Option configures a Manager.
type Option func(*Manager)

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) Option {
	return func(m *Manager) {
		if l != nil {
			m.log = l
		}
	}
}

// WithClock injects a time source, for deterministic windows in tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// WithCostModel overrides the cost model (default: a PricingModel from config).
func WithCostModel(c CostModel) Option {
	return func(m *Manager) {
		if c != nil {
			m.cost = c
		}
	}
}

// WithPolicy overrides the enforcement policy (default: resolved from config).
func WithPolicy(p Policy) Option {
	return func(m *Manager) {
		if p != nil {
			m.policy = p
		}
	}
}

// NewManager constructs a budget Manager from configuration. It validates the
// config and resolves the policy by name, failing fast.
func NewManager(cfg Config, opts ...Option) (*Manager, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	policy, err := DefaultPolicyRegistry().Build(cfg.Policy, cfg)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		cfg:     cfg,
		cost:    NewPricingModel(cfg.Pricing, cfg.EstimatedInputTokens, cfg.ExpectedOutputTokens),
		policy:  policy,
		log:     logger.Nop(),
		clock:   time.Now,
		budgets: make(map[string]*managedBudget),
		ledger:  make(map[string][]UsageRecord),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// CostModel exposes the engine's cost model for estimation/reporting reuse.
func (m *Manager) CostModel() CostModel { return m.cost }

// Policy returns the active enforcement policy name.
func (m *Manager) Policy() string { return m.policy.Name() }

func key(scope Scope, id string) string { return string(scope) + ":" + id }

// SetBudget registers or replaces a budget's limit. Existing usage is preserved
// when only the limit changes, so tightening a limit takes effect immediately
// without wiping the day's spend.
func (m *Manager) SetBudget(b Budget) error {
	if !b.valid() {
		return ErrInvalidBudget
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(b.Scope, b.ID)
	if mb, ok := m.budgets[k]; ok {
		mb.desc = b
		return nil
	}
	m.budgets[k] = &managedBudget{desc: b, resetAt: nextReset(m.clock())}
	return nil
}

// Budget returns a budget's current status, provisioning it from the scope's
// default daily limit if it has not been explicitly registered. The bool is false
// only for an invalid scope.
func (m *Manager) Budget(scope Scope, id string) (BudgetStatus, bool) {
	if !scope.valid() {
		return BudgetStatus{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolve(scope, id).status(), true
}

// resolve returns the managed budget for (scope, id), creating it from the
// default limit if absent and rolling over an elapsed window. Caller holds mu.
func (m *Manager) resolve(scope Scope, id string) *managedBudget {
	k := key(scope, id)
	mb, ok := m.budgets[k]
	if !ok {
		mb = &managedBudget{
			desc:    Budget{ID: id, Scope: scope, DailyLimit: m.cfg.defaultLimitFor(scope)},
			resetAt: nextReset(m.clock()),
		}
		m.budgets[k] = mb
	}
	mb.rollover(m.clock())
	return mb
}

// Authorize runs the decision pipeline for a candidate request: estimate cost,
// read the budget, and apply the policy. It mutates nothing — no funds are held —
// so it is a fast, idempotent read. The caller dispatches on Allow/Downgrade
// (using Decision.Model) then Commits the actual cost.
func (m *Manager) Authorize(_ context.Context, req AuthorizeRequest) (Decision, error) {
	if !req.Scope.valid() {
		return Decision{}, ErrInvalidScope
	}
	estimate := m.cost.Estimate(req.Model, req.InputTokens, req.ExpectedOutputTokens)

	m.mu.Lock()
	status := m.resolve(req.Scope, req.BudgetID).status()
	m.mu.Unlock()

	decision := m.policy.Decide(PolicyInput{
		Request:   req,
		Status:    status,
		Estimate:  estimate,
		CostModel: m.cost,
		Config:    m.cfg,
	})
	m.log.Debug("budget decision",
		logger.String("scope", string(req.Scope)),
		logger.String("budget", req.BudgetID),
		logger.String("outcome", string(decision.Outcome)),
		logger.String("model", decision.Model),
		logger.String("policy", decision.Policy),
	)
	return decision, nil
}

// Commit records the actual cost of a completed request against its budget and
// appends it to the usage ledger. It is the single mutation point and is atomic.
// If the record carries no ActualCost, the cost is computed from its token usage
// via the cost model. The timestamp is stamped from the clock when unset.
func (m *Manager) Commit(_ context.Context, rec UsageRecord) error {
	if !rec.Scope.valid() {
		return ErrInvalidScope
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = m.clock()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	mb := m.resolve(rec.Scope, rec.BudgetID)
	mb.usage += rec.ActualCost

	k := key(rec.Scope, rec.BudgetID)
	led := append(m.ledger[k], rec)
	if len(led) > m.cfg.LedgerSize {
		led = led[len(led)-m.cfg.LedgerSize:]
	}
	m.ledger[k] = led
	return nil
}

// CommitUsage is a convenience that computes the actual cost from provider-
// reported usage and commits it, returning the recorded cost.
func (m *Manager) CommitUsage(ctx context.Context, scope Scope, id, providerName, model string, usage provider.Usage, estimated float64) (float64, error) {
	actual := m.cost.Actual(model, usage)
	err := m.Commit(ctx, UsageRecord{
		Scope:         scope,
		BudgetID:      id,
		Provider:      providerName,
		Model:         model,
		Tokens:        usage.TotalTokens,
		EstimatedCost: estimated,
		ActualCost:    actual,
	})
	return actual, err
}

// Usage returns a copy of the recent usage records for a budget, newest last.
func (m *Manager) Usage(scope Scope, id string) []UsageRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	led := m.ledger[key(scope, id)]
	out := make([]UsageRecord, len(led))
	copy(out, led)
	return out
}
