package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// Default monitor parameters.
const (
	DefaultProbeInterval = 15 * time.Second
	DefaultProbeTimeout  = 5 * time.Second
)

// errProbeUnhealthy is the failure returned when a provider's health check
// succeeds transport-wise but reports a non-healthy state.
var errProbeUnhealthy = errors.New("provider reported unhealthy")

// ProviderSource is the view of the Provider Layer the monitor needs to discover
// and resolve providers to probe. *provider.Manager satisfies it.
type ProviderSource interface {
	ListProviders() []string
	GetProvider(name string) (provider.LLMProvider, error)
}

// MonitorConfig configures the background health monitor.
type MonitorConfig struct {
	// Interval is the time between probe rounds.
	Interval time.Duration `json:"interval"`
	// Timeout bounds a single provider probe.
	Timeout time.Duration `json:"timeout"`
}

// WithDefaults fills non-positive fields with defaults.
func (c MonitorConfig) WithDefaults() MonitorConfig {
	if c.Interval <= 0 {
		c.Interval = DefaultProbeInterval
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultProbeTimeout
	}
	return c
}

// Monitor is the background service that periodically probes every provider's
// health check through its circuit breaker, updating the health registry and
// emitting events. Because probes flow through the same per-provider breaker as
// live traffic, they both drive its state and drive automatic recovery: once the
// cooldown elapses, a probe is admitted in half-open and its success closes the
// breaker.
//
// The Monitor implements no failover and no metrics export — it only observes and
// records health.
type Monitor struct {
	cfg      MonitorConfig
	source   ProviderSource
	breakers *Manager
	registry *Registry
	clock    func() time.Time
	log      logger.Logger

	listenersMu sync.RWMutex
	listeners   []Listener

	lifecycleMu sync.Mutex
	running     bool
	stopCh      chan struct{}
	doneCh      chan struct{}
}

// MonitorOption configures a Monitor.
type MonitorOption func(*Monitor)

// WithMonitorClock injects a time source for deterministic tests.
func WithMonitorClock(now func() time.Time) MonitorOption {
	return func(m *Monitor) {
		if now != nil {
			m.clock = now
		}
	}
}

// WithMonitorLogger injects a structured logger.
func WithMonitorLogger(l logger.Logger) MonitorOption {
	return func(m *Monitor) {
		if l != nil {
			m.log = l
		}
	}
}

// WithListener registers a health event listener at construction.
func WithListener(l Listener) MonitorOption {
	return func(m *Monitor) {
		if l != nil {
			m.listeners = append(m.listeners, l)
		}
	}
}

// NewMonitor constructs a health monitor over a provider source, the shared
// breaker manager, and a health registry.
func NewMonitor(cfg MonitorConfig, source ProviderSource, breakers *Manager, registry *Registry, opts ...MonitorOption) *Monitor {
	m := &Monitor{
		cfg:      cfg.WithDefaults(),
		source:   source,
		breakers: breakers,
		registry: registry,
		clock:    time.Now,
		log:      logger.Nop(),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// AddListener registers a health event listener.
func (m *Monitor) AddListener(l Listener) {
	if l == nil {
		return
	}
	m.listenersMu.Lock()
	m.listeners = append(m.listeners, l)
	m.listenersMu.Unlock()
}

// Start launches the background probe loop. It is a no-op if already running.
func (m *Monitor) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if m.running {
		return
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	go m.loop(ctx, m.stopCh, m.doneCh)
	m.log.Info("health monitor started", logger.String("interval", m.cfg.Interval.String()))
}

// Stop halts the background probe loop and waits for it to finish. Safe to call
// more than once.
func (m *Monitor) Stop() {
	m.lifecycleMu.Lock()
	if !m.running {
		m.lifecycleMu.Unlock()
		return
	}
	m.running = false
	stop, done := m.stopCh, m.doneCh
	m.lifecycleMu.Unlock()

	close(stop)
	<-done
	m.log.Info("health monitor stopped")
}

func (m *Monitor) loop(ctx context.Context, stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			m.CheckNow(ctx)
		}
	}
}

// CheckNow probes every provider once, concurrently, and returns after all probes
// complete. It is used by the background loop and can also be called on demand
// (e.g. at startup or in tests) for a deterministic, synchronous round.
func (m *Monitor) CheckNow(ctx context.Context) {
	var wg sync.WaitGroup
	for _, name := range m.source.ListProviders() {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			m.probe(ctx, name)
		}(name)
	}
	wg.Wait()
}

// probe performs a single health probe of one provider through its breaker,
// updates the registry, and emits any state-change events.
func (m *Monitor) probe(ctx context.Context, name string) {
	p, err := m.source.GetProvider(name)
	if err != nil {
		m.log.Warn("health probe: provider unavailable", logger.String("provider", name), logger.Err(err))
		return
	}

	// Optimistically assume a not-yet-seen provider was healthy, so a first probe
	// that finds it down emits ProviderDown but a first healthy probe is silent.
	prev := StateClosed
	if rec, ok := m.registry.Record(name); ok {
		prev = rec.State
	}

	breaker := m.breakers.Breaker(name)

	probeCtx, cancel := m.probeContext(ctx)
	defer cancel()

	start := m.clock()
	probeErr := breaker.Execute(probeCtx, func(cctx context.Context) error {
		status, herr := p.HealthCheck(cctx)
		if herr != nil {
			return herr
		}
		if !status.Healthy() {
			return errProbeUnhealthy
		}
		return nil
	})
	now := m.clock()

	// A breaker rejection (open/half-open limit) means the probe did not run; we
	// record the breaker state but leave success/failure timestamps untouched.
	gated := errors.Is(probeErr, ErrCircuitOpen) || errors.Is(probeErr, ErrHalfOpenLimitReached)

	rec, _ := m.registry.Record(name)
	rec.Provider = name
	if !gated {
		rec.Latency = now.Sub(start)
		if probeErr == nil {
			rec.LastSuccess = now
			rec.LastError = ""
		} else {
			rec.LastFailure = now
			rec.LastError = probeErr.Error()
		}
	}
	rec.State = breaker.State()
	rec.Available = rec.State != StateOpen
	rec.CheckedAt = now
	m.registry.set(rec)

	m.emitTransition(name, prev, rec.State, now)
}

// emitTransition emits the appropriate events for a state change.
func (m *Monitor) emitTransition(name string, from, to State, at time.Time) {
	if from == to {
		return
	}
	m.emit(Event{Type: EventStateChanged, Provider: name, From: from, To: to, At: at})
	switch {
	case to == StateOpen:
		m.emit(Event{Type: EventProviderDown, Provider: name, From: from, To: to, At: at})
	case to == StateClosed:
		m.emit(Event{Type: EventProviderRecovered, Provider: name, From: from, To: to, At: at})
	}
}

func (m *Monitor) emit(e Event) {
	m.listenersMu.RLock()
	listeners := m.listeners
	m.listenersMu.RUnlock()
	for _, l := range listeners {
		l(e)
	}
}

// probeContext derives a per-probe context bounded by the configured timeout.
func (m *Monitor) probeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.cfg.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, m.cfg.Timeout)
}
