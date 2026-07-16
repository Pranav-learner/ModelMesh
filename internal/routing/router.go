package routing

import (
	"context"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// ProviderSource is the narrow view of the Provider Layer that the router needs
// to enumerate candidates. *provider.Manager satisfies it. Depending on this
// interface (rather than the concrete manager) keeps routing decoupled from the
// Provider Layer's internals and trivially unit-testable with a fake source.
type ProviderSource interface {
	// ListProviders returns the names of registered providers.
	ListProviders() []string
	// ListModels returns the model catalog for a provider.
	ListModels(ctx context.Context, name string) ([]provider.ModelInfo, error)
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

	selected := ranked[0]
	decision := RoutingDecision{
		Selected:    selected,
		Candidates:  ranked,
		Strategy:    m.strategy.Name(),
		Explanation: buildExplanation(m.strategy.Name(), ranked),
	}

	m.log.Debug("routing decision",
		logger.String("strategy", decision.Strategy),
		logger.String("provider", selected.Provider),
		logger.String("model", selected.Model),
		logger.Int("candidates", len(ranked)),
	)
	return decision, nil
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
// candidates, marking the first as selected.
func buildExplanation(strategy string, ranked []Candidate) RoutingExplanation {
	ce := make([]CandidateExplanation, len(ranked))
	for i, c := range ranked {
		ce[i] = CandidateExplanation{
			Provider: c.Provider,
			Model:    c.Model,
			Weight:   c.Weight,
			Score:    c.Score,
			Selected: i == 0,
			Reason:   c.Reason,
		}
	}
	reason := "no candidates"
	if len(ranked) > 0 {
		reason = "selected top-ranked candidate (scoring not yet applied)"
	}
	return RoutingExplanation{
		Strategy:   strategy,
		Reason:     reason,
		Considered: len(ranked),
		Candidates: ce,
	}
}
