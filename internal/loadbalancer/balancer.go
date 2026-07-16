package loadbalancer

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// LoadBalancer is the subsystem façade. It wires an instance registry to a
// selection Strategy and closes the feedback loop. New strategies plug in behind
// this interface without changing callers.
type LoadBalancer interface {
	// Select chooses the best eligible instance for the request.
	Select(ctx context.Context, req Request) (Selection, error)
	// Register adds an instance to the pool.
	Register(inst Instance) error
	// Remove deregisters an instance by ID.
	Remove(id string) error
	// Update feeds a completed request's observation back into the pool.
	Update(obs Observation) error
	// Statistics returns a snapshot of the pool and its runtime state.
	Statistics() Statistics
}

// Compile-time assertion that Balancer implements LoadBalancer.
var _ LoadBalancer = (*Balancer)(nil)

// Balancer is the concrete LoadBalancer. It owns an InstanceRegistry and a
// Strategy; selection is a pure pipeline over registry snapshots, and mutation of
// counters happens on the registry under its own lock.
type Balancer struct {
	registry   *InstanceRegistry
	strategy   Strategy
	log        logger.Logger
	metrics    Metrics
	health     HealthSource
	selections atomic.Uint64
}

// Option configures a Balancer.
type Option func(*Balancer)

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) Option {
	return func(b *Balancer) {
		if l != nil {
			b.log = l
		}
	}
}

// WithMetrics injects an observability sink. A nil sink is ignored (default Nop).
func WithMetrics(m Metrics) Option {
	return func(b *Balancer) {
		if m != nil {
			b.metrics = m
		}
	}
}

// WithHealthSource injects the provider-health seam used to gate unhealthy
// providers out of selection (e.g. resilience.Registry). A nil source is ignored.
func WithHealthSource(h HealthSource) Option {
	return func(b *Balancer) {
		if h != nil {
			b.health = h
		}
	}
}

// WithClock injects a time source for deterministic LastUsed stamps in tests.
func WithClock(now func() time.Time) Option {
	return func(b *Balancer) {
		if now != nil {
			b.registry.clock = now
		}
	}
}

// New constructs a Balancer over an explicitly injected strategy and config. It
// panics only on a nil strategy, a programming error at the composition root.
func New(cfg Config, strategy Strategy, opts ...Option) *Balancer {
	if strategy == nil {
		panic("loadbalancer: Strategy must not be nil")
	}
	cfg = cfg.WithDefaults()
	b := &Balancer{
		registry: NewInstanceRegistry(cfg.LatencyWindow, time.Now),
		strategy: strategy,
		log:      logger.Nop(),
		metrics:  NopMetrics{},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Build constructs a Balancer from configuration, resolving the strategy by name
// via the default strategy registry. It validates the config and fails fast,
// mirroring routing.Build.
func Build(cfg Config, opts ...Option) (*Balancer, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	strategy, err := DefaultRegistry().Build(cfg.Strategy, cfg)
	if err != nil {
		return nil, err
	}
	return New(cfg, strategy, opts...), nil
}

// Registry exposes the underlying instance registry for the operator-facing
// lifecycle methods (Enable / Disable / Discover / SetHealth).
func (b *Balancer) Registry() *InstanceRegistry { return b.registry }

// Strategy returns the active strategy name.
func (b *Balancer) Strategy() string { return b.strategy.Name() }

// Register adds an instance to the pool.
func (b *Balancer) Register(inst Instance) error { return b.registry.Register(inst) }

// Remove deregisters an instance by ID.
func (b *Balancer) Remove(id string) error { return b.registry.Deregister(id) }

// Enable marks an instance eligible for selection.
func (b *Balancer) Enable(id string) error { return b.registry.Enable(id) }

// Disable marks an instance ineligible without discarding its stats.
func (b *Balancer) Disable(id string) error { return b.registry.Disable(id) }

// Discover reconciles the pool against a desired instance set.
func (b *Balancer) Discover(desired []Instance) error { return b.registry.Discover(desired) }

// Update feeds a completed request's observation back into the pool.
func (b *Balancer) Update(obs Observation) error { return b.registry.Update(obs) }

// Select runs the selection pipeline: enumerate eligible candidates, delegate the
// choice to the strategy, mark the chosen instance selected, and return it. It
// returns ErrNoInstances when nothing is eligible.
func (b *Balancer) Select(ctx context.Context, req Request) (Selection, error) {
	candidates := b.candidates(req)
	if len(candidates) == 0 {
		return Selection{}, ErrNoInstances
	}

	chosen, err := b.strategy.Pick(ctx, req, candidates)
	if err != nil {
		return Selection{}, err
	}

	stats, ok := b.registry.markSelected(chosen.Instance.ID)
	if !ok {
		// The instance was removed between enumeration and marking; nothing to do.
		return Selection{}, ErrNoInstances
	}

	b.selections.Add(1)
	b.metrics.RecordSelection(b.strategy.Name(), chosen.Instance.Provider, chosen.Instance.ID)
	b.log.Debug("instance selected",
		logger.String("strategy", b.strategy.Name()),
		logger.String("provider", chosen.Instance.Provider),
		logger.String("instance", chosen.Instance.ID),
		logger.String("region", chosen.Instance.Region),
	)
	return Selection{Instance: chosen.Instance, Strategy: b.strategy.Name(), Stats: stats}, nil
}

// candidates enumerates the eligible instances for a request, sorted by ID so the
// strategy sees a deterministic order.
func (b *Balancer) candidates(req Request) []Candidate {
	stats := b.registry.List()
	out := make([]Candidate, 0, len(stats))
	for _, s := range stats {
		if req.Provider != "" && s.Provider != req.Provider {
			continue
		}
		if !b.eligible(s) {
			continue
		}
		desc, ok := b.registry.descriptor(s.ID)
		if !ok {
			continue
		}
		out = append(out, Candidate{Instance: desc, Stats: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance.ID < out[j].Instance.ID })
	return out
}

// eligible reports whether an instance may currently be selected: it must be
// enabled, not itself unhealthy, and — when a HealthSource is wired — its provider
// must not be reported unhealthy. Unknown provider health is treated optimistically
// as routable, matching the routing engine's stance.
func (b *Balancer) eligible(s InstanceStats) bool {
	if !s.Enabled || s.Health == provider.HealthStateUnhealthy {
		return false
	}
	if b.health != nil {
		if st, ok := b.health.Health(s.Provider); ok && st.State == provider.HealthStateUnhealthy {
			return false
		}
	}
	return true
}
