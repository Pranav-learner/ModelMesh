package routing

import (
	"context"
	"fmt"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// ProviderSource is the narrow view of the Provider Layer that the router needs
// to enumerate candidates, validate them, and resolve the selected provider.
// *provider.Manager satisfies it. Depending on this interface (rather than the
// concrete manager) keeps routing decoupled from the Provider Layer's internals
// and trivially unit-testable with a fake source.
type ProviderSource interface {
	// ListProviders returns the names of registered providers.
	ListProviders() []string
	// ListModels returns the model catalog for a provider.
	ListModels(ctx context.Context, name string) ([]provider.ModelInfo, error)
	// GetProvider resolves a registered provider by name. A non-nil error means
	// the provider is not registered/available (which the router treats as a
	// validation failure for fallback purposes).
	GetProvider(name string) (provider.LLMProvider, error)
}

// Router is the routing abstraction: given a routing context it returns a
// decision naming the selected provider/model and the ordered candidate list.
// The concrete Manager implements it; callers depend on the interface.
type Router interface {
	// Route selects a provider/model for the request described by rc.
	Route(ctx context.Context, rc RoutingContext) (RoutingDecision, error)
	// Strategy returns the active strategy name.
	Strategy() string
}

// Compile-time assertion that Manager implements Router.
var _ Router = (*Manager)(nil)

// Manager is the concrete routing manager. It enumerates candidates from the
// Provider Layer, delegates ordering to the active Strategy, and assembles the
// RoutingDecision. It holds no health or scoring state in this phase.
type Manager struct {
	source   ProviderSource
	strategy Strategy
	cfg      Config
	log      logger.Logger
	metrics  Metrics
	clock    func() time.Time
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

// WithMetrics injects a routing metrics sink. A nil sink is ignored (the default
// is NopMetrics).
func WithMetrics(mx Metrics) Option {
	return func(m *Manager) {
		if mx != nil {
			m.metrics = mx
		}
	}
}

// WithClock injects a time source, for deterministic durations in tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// NewManager constructs a routing Manager from an explicitly injected source,
// strategy, and config. It panics only on a nil source or strategy, which are
// programming errors at the composition root.
func NewManager(source ProviderSource, strategy Strategy, cfg Config, opts ...Option) *Manager {
	if source == nil {
		panic("routing: ProviderSource must not be nil")
	}
	if strategy == nil {
		panic("routing: Strategy must not be nil")
	}
	m := &Manager{
		source:   source,
		strategy: strategy,
		cfg:      cfg.WithDefaults(),
		log:      logger.Nop(),
		metrics:  NopMetrics{},
		clock:    time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Build constructs a routing Manager from configuration, using the default
// strategy registry to instantiate the configured strategy. It validates the
// config and fails fast, mirroring the Provider Layer's Bootstrap flow.
func Build(source ProviderSource, cfg Config, opts ...Option) (*Manager, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	strategy, err := DefaultRegistry().Build(cfg.Strategy, cfg)
	if err != nil {
		return nil, err
	}
	return NewManager(source, strategy, cfg, opts...), nil
}

// Strategy returns the active strategy name.
func (m *Manager) Strategy() string { return m.strategy.Name() }

// Route enumerates eligible candidates, delegates ordering to the strategy, and
// returns the decision. It performs no scoring or health checks in this phase;
// the selected candidate is the strategy's top-ranked one.
func (m *Manager) Route(ctx context.Context, rc RoutingContext) (RoutingDecision, error) {
	candidates := m.enumerate(ctx, rc)
	if len(candidates) == 0 {
		return RoutingDecision{}, ErrNoCandidates
	}

	ranked, err := m.strategy.Rank(ctx, rc, candidates)
	if err != nil {
		return RoutingDecision{}, err
	}
	if len(ranked) == 0 {
		return RoutingDecision{}, ErrNoCandidates
	}

	// If the strategy can explain its weighting, surface it in the explanation.
	var weights map[string]float64
	if ex, ok := m.strategy.(Explainer); ok {
		weights = ex.NormalizedWeights()
	}

	selected := ranked[0]
	decision := RoutingDecision{
		Selected:    selected,
		Candidates:  ranked,
		Strategy:    m.strategy.Name(),
		Explanation: buildExplanation(m.strategy.Name(), ranked, weights),
	}

	m.log.Debug("routing decision",
		logger.String("strategy", decision.Strategy),
		logger.String("provider", selected.Provider),
		logger.String("model", selected.Model),
		logger.Int("candidates", len(ranked)),
	)
	return decision, nil
}

// Selection is the outcome of Select: the resolved provider to dispatch to, the
// candidate actually chosen (which may differ from the top-ranked one if fallback
// occurred), the full scoring decision, and fallback bookkeeping.
type Selection struct {
	// Provider is the resolved, validated provider ready for dispatch.
	Provider provider.LLMProvider
	// Selected is the candidate chosen after validation/fallback.
	Selected Candidate
	// Decision is the full scoring decision (ranked candidates + explanation).
	Decision RoutingDecision
	// FallbackUsed is true when the top-ranked candidate failed validation and a
	// lower-ranked one was chosen.
	FallbackUsed bool
	// Attempts is how many candidates were validated before one succeeded.
	Attempts int
	// Duration is the wall-clock time taken to reach the selection.
	Duration time.Duration
}

// Select performs the complete routing decision: it ranks candidates (Route),
// then validates them in rank order against the Provider Layer, selecting the
// highest-ranked candidate that passes validation. If the top candidate fails
// validation it falls back to the next, and so on. It emits a structured decision
// log and records metrics.
//
// Fallback here is intentionally simple — it advances to the next VALID candidate
// on a validation failure. It is NOT a circuit breaker and performs NO retries
// against a provider that errors at dispatch time; that resilience arrives in
// Phase 4.
func (m *Manager) Select(ctx context.Context, rc RoutingContext) (*Selection, error) {
	start := m.clock()

	decision, err := m.Route(ctx, rc)
	if err != nil {
		m.metrics.RecordDecision(DecisionRecord{Failed: true, Duration: m.clock().Sub(start)})
		m.log.Warn("routing failed",
			logger.String("request_id", rc.RequestID),
			logger.String("capability", string(rc.Capability)),
			logger.Err(err),
		)
		return nil, err
	}

	for i, candidate := range decision.Candidates {
		p, verr := m.validate(ctx, rc, candidate)
		if verr != nil {
			m.log.Warn("candidate failed validation; falling back",
				logger.String("request_id", rc.RequestID),
				logger.String("provider", candidate.Provider),
				logger.String("model", candidate.Model),
				logger.Err(verr),
			)
			continue
		}

		sel := &Selection{
			Provider:     p,
			Selected:     candidate,
			Decision:     decision,
			FallbackUsed: i > 0,
			Attempts:     i + 1,
			Duration:     m.clock().Sub(start),
		}
		m.metrics.RecordDecision(DecisionRecord{
			Provider:   candidate.Provider,
			Model:      candidate.Model,
			Score:      candidate.Score,
			Duration:   sel.Duration,
			Fallback:   sel.FallbackUsed,
			Candidates: len(decision.Candidates),
		})
		m.logDecision(rc, sel)
		return sel, nil
	}

	// Every ranked candidate failed validation.
	m.metrics.RecordDecision(DecisionRecord{Failed: true, Duration: m.clock().Sub(start), Candidates: len(decision.Candidates)})
	m.log.Error("routing exhausted all candidates",
		logger.String("request_id", rc.RequestID),
		logger.Int("candidates", len(decision.Candidates)),
	)
	return nil, fmt.Errorf("%w: %d candidate(s) considered", ErrNoValidProvider, len(decision.Candidates))
}

// validate checks a candidate against the Provider Layer: the provider must be
// registered/available (which implies enabled and startup-validated, since only
// such providers are in the registry), and must still advertise the requested
// model with the required capability. It returns the resolved provider on success
// or a meaningful error on failure.
func (m *Manager) validate(ctx context.Context, rc RoutingContext, c Candidate) (provider.LLMProvider, error) {
	p, err := m.source.GetProvider(c.Provider)
	if err != nil {
		return nil, err // already a meaningful provider error (e.g. ErrProviderNotFound)
	}

	capability := rc.Capability
	if capability == "" {
		capability = provider.CapabilityChat
	}
	models, err := m.source.ListModels(ctx, c.Provider)
	if err != nil {
		return nil, fmt.Errorf("model discovery for %q: %w", c.Provider, err)
	}
	if !provider.ModelSupported(models, c.Model, capability) {
		return nil, fmt.Errorf("%w: %q on provider %q", provider.ErrUnsupportedModel, c.Model, c.Provider)
	}
	return p, nil
}

// logDecision emits the structured routing decision log with the fields future
// phases (and operators) rely on.
func (m *Manager) logDecision(rc RoutingContext, sel *Selection) {
	m.log.Info("routing decision",
		logger.String("request_id", rc.RequestID),
		logger.String("prompt_summary", rc.Summary),
		logger.String("strategy", sel.Decision.Strategy),
		logger.Int("available_providers", len(m.source.ListProviders())),
		logger.Int("candidates", len(sel.Decision.Candidates)),
		logger.Any("candidate_scores", candidateScores(sel.Decision.Candidates)),
		logger.String("selected_provider", sel.Selected.Provider),
		logger.String("selected_model", sel.Selected.Model),
		logger.Any("selected_score", sel.Selected.Score),
		logger.String("routing_duration", sel.Duration.String()),
		logger.String("reason", sel.Selected.Reason),
		logger.Bool("fallback_used", sel.FallbackUsed),
	)
}

// candidateScores renders a compact provider/model=score list for logging.
func candidateScores(candidates []Candidate) []string {
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = fmt.Sprintf("%s/%s=%.3f", c.Provider, c.Model, c.Score)
	}
	return out
}

// enumerate builds the eligible candidate set from the Provider Layer, filtered
// by the required capability and the caller's constraints. A provider whose model
// discovery fails is skipped (logged), so one bad provider does not fail routing;
// if that leaves no candidates, Route returns ErrNoCandidates.
func (m *Manager) enumerate(ctx context.Context, rc RoutingContext) []Candidate {
	capability := rc.Capability
	if capability == "" {
		capability = provider.CapabilityChat
	}

	var out []Candidate
	for _, name := range m.source.ListProviders() {
		if !rc.Constraints.allowsProvider(name) {
			continue
		}
		models, err := m.source.ListModels(ctx, name)
		if err != nil {
			m.log.Warn("routing: model discovery failed; skipping provider",
				logger.String("provider", name), logger.Err(err))
			continue
		}
		for _, mi := range models {
			if !mi.Supports(capability) {
				continue
			}
			if rc.Model != "" && mi.ID != rc.Model {
				continue
			}
			if !rc.Constraints.allowsModel(mi.ID) {
				continue
			}
			out = append(out, Candidate{Provider: name, Model: mi.ID})
		}
	}
	return out
}

// buildExplanation constructs a structured explanation from the ranked
// candidates (best first), carrying each candidate's factor breakdown, rank, and
// reason, plus the normalized factor weights when the strategy provided them.
func buildExplanation(strategy string, ranked []Candidate, weights map[string]float64) RoutingExplanation {
	ce := make([]CandidateExplanation, len(ranked))
	for i, c := range ranked {
		ce[i] = CandidateExplanation{
			Provider: c.Provider,
			Model:    c.Model,
			Weight:   c.Weight,
			Factors:  c.Factors,
			Score:    c.Score,
			Rank:     i + 1,
			Selected: i == 0,
			Reason:   c.Reason,
		}
	}
	reason := "no candidates"
	if len(ranked) > 0 {
		reason = ranked[0].Reason
	}
	return RoutingExplanation{
		Strategy:   strategy,
		Reason:     reason,
		Weights:    weights,
		Considered: len(ranked),
		Candidates: ce,
	}
}
