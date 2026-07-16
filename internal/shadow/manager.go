package shadow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

// correlationID extracts the primary request's correlation ID from the context,
// captured into shadow metadata before the shadow detaches to its own context.
func correlationID(ctx context.Context) string {
	if id, ok := tracing.RequestIDFromContext(ctx); ok {
		return id
	}
	return ""
}

// ProviderSource lists and resolves providers, so the manager can select a
// secondary independently of the primary. *provider.Manager satisfies it.
type ProviderSource interface {
	ListProviders() []string
	GetProvider(name string) (provider.LLMProvider, error)
}

// Manager is the shadow-traffic entry point. It decides whether to shadow a
// request, clones it, selects a secondary provider, and dispatches the shadow
// asynchronously — never affecting the primary response.
type Manager struct {
	cfg       Config
	policy    Policy
	selector  Selector
	providers ProviderSource
	tracker   *tracker
	log       logger.Logger
	clock     func() time.Time
	idgen     func() string
	sampler   func() float64

	wg sync.WaitGroup // in-flight shadow goroutines
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

// WithSelector overrides the secondary-provider selector.
func WithSelector(s Selector) Option {
	return func(m *Manager) {
		if s != nil {
			m.selector = s
		}
	}
}

// WithSampler injects the [0,1) random source used by the fixed-percentage policy.
// It is the seam that makes sampling deterministic in tests.
func WithSampler(sampler func() float64) Option {
	return func(m *Manager) {
		if sampler != nil {
			m.sampler = sampler
		}
	}
}

// WithClock injects a time source, for deterministic timing in tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// WithPolicy overrides the sampling policy (bypassing config resolution).
func WithPolicy(p Policy) Option {
	return func(m *Manager) {
		if p != nil {
			m.policy = p
		}
	}
}

// WithIDGenerator overrides the execution ID generator, for deterministic tests.
func WithIDGenerator(gen func() string) Option {
	return func(m *Manager) {
		if gen != nil {
			m.idgen = gen
		}
	}
}

// New constructs a shadow Manager from configuration and a provider source. It
// validates the config and resolves the policy by name, failing fast.
func New(cfg Config, providers ProviderSource, opts ...Option) (*Manager, error) {
	if providers == nil {
		return nil, fmt.Errorf("%w: provider source must not be nil", ErrInvalidConfig)
	}
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	m := &Manager{
		cfg:       cfg,
		selector:  FirstOtherSelector{},
		providers: providers,
		tracker:   newTracker(cfg.MaxTrackedExecutions),
		log:       logger.Nop(),
		clock:     time.Now,
		idgen:     newID,
	}
	for _, opt := range opts {
		opt(m)
	}
	// Resolve the policy after options so an injected sampler is honored. An
	// explicitly injected policy (WithPolicy) wins.
	if m.policy == nil {
		policy, err := DefaultPolicyRegistry().Build(cfg.Policy, cfg, m.sampler)
		if err != nil {
			return nil, err
		}
		m.policy = policy
	}
	return m, nil
}

// Policy returns the active policy name.
func (m *Manager) Policy() string { return m.policy.Name() }

// Shadow considers a request for shadow traffic. When the policy samples it and a
// secondary provider is available, it clones the request, dispatches the shadow
// asynchronously, and returns the execution handle and true. Otherwise it returns
// (nil, false). It never blocks on the shadow request and never returns an error —
// shadowing must not affect the primary path.
func (m *Manager) Shadow(ctx context.Context, req provider.ChatRequest, primary Target) (*ShadowExecution, bool) {
	m.tracker.evaluated()

	dec := m.policy.Decide(ctx, req)
	if !dec.Sample {
		return nil, false
	}
	m.tracker.sampled()

	target, ok := m.selector.Select(primary, m.candidates(primary, req))
	if !ok {
		m.tracker.skipped()
		m.log.Debug("shadow: no secondary provider available", logger.String("primary", primary.Provider))
		return nil, false
	}

	exec := newExecution(
		m.idgen(),
		ShadowRequest{ID: m.idgen(), Request: cloneRequest(req), Target: target},
		ShadowMetadata{
			CorrelationID: correlationID(ctx),
			Primary:       primary,
			Policy:        m.policy.Name(),
			SampleRate:    dec.Rate,
			CreatedAt:     m.clock(),
		},
	)

	m.tracker.dispatched(exec)
	m.wg.Add(1)
	go m.run(exec)
	return exec, true
}

// candidates builds the secondary target set: every registered provider paired
// with the model the primary used (falling back to the request's model).
func (m *Manager) candidates(primary Target, req provider.ChatRequest) []Target {
	model := primary.Model
	if model == "" {
		model = req.Model
	}
	names := m.providers.ListProviders()
	out := make([]Target, 0, len(names))
	for _, name := range names {
		out = append(out, Target{Provider: name, Model: model})
	}
	return out
}

// run executes a shadow request on a detached context, fully isolating it from the
// primary: the primary's cancellation cannot reach it, and its failures/panics are
// contained and recorded, never propagated.
func (m *Manager) run(exec *ShadowExecution) {
	defer m.wg.Done()

	start := m.clock()
	result := ShadowResult{StartedAt: start}

	defer func() {
		if r := recover(); r != nil {
			result.Success = false
			result.Err = fmt.Sprintf("shadow panic: %v", r)
			m.finish(exec, &result, start)
			m.log.Warn("shadow: recovered panic", logger.String("execution", exec.ID))
		}
	}()

	// Detached context: independent lifetime + timeout, so the shadow never
	// affects (and is never affected by) the primary request's context.
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ShadowTimeout)
	defer cancel()

	p, err := m.providers.GetProvider(exec.Request.Target.Provider)
	if err != nil {
		result.Err = err.Error()
		m.finish(exec, &result, start)
		return
	}

	shadowReq := exec.Request.Request
	shadowReq.Model = exec.Request.Target.Model
	resp, cerr := p.Chat(ctx, shadowReq)
	if cerr != nil {
		result.Err = cerr.Error()
	} else {
		result.Response = resp
		result.Success = true
	}
	m.finish(exec, &result, start)
}

// finish stamps timing, records completion, and completes the execution.
func (m *Manager) finish(exec *ShadowExecution, result *ShadowResult, start time.Time) {
	end := m.clock()
	result.CompletedAt = end
	result.Latency = end.Sub(start)
	exec.complete(*result)
	m.tracker.completed(result.Success)
	m.log.Debug("shadow completed",
		logger.String("execution", exec.ID),
		logger.String("target", exec.Request.Target.Provider),
		logger.Bool("success", result.Success),
	)
}

// Wait blocks until all in-flight shadow requests have completed. It is used at
// shutdown and in tests; production callers never wait on shadow traffic.
func (m *Manager) Wait() { m.wg.Wait() }

// Stats returns a snapshot of the execution counters.
func (m *Manager) Stats() Stats { return m.tracker.snapshot() }

// Recent returns the retained recent executions (newest last).
func (m *Manager) Recent() []*ShadowExecution { return m.tracker.recentExecutions() }
