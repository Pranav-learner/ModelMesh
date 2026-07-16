package resilience

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// stubProvider is a controllable provider whose health can be toggled at runtime.
type stubProvider struct {
	name         string
	healthy      atomic.Bool
	transportErr atomic.Bool // when true, HealthCheck returns an error
	sleep        time.Duration
	checks       atomic.Int32
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) HealthCheck(context.Context) (provider.HealthStatus, error) {
	p.checks.Add(1)
	if p.sleep > 0 {
		time.Sleep(p.sleep)
	}
	if p.transportErr.Load() {
		return provider.HealthStatus{State: provider.HealthStateUnknown}, provider.ErrProviderUnavailable
	}
	if p.healthy.Load() {
		return provider.HealthStatus{State: provider.HealthStateHealthy}, nil
	}
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}

func (p *stubProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *stubProvider) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (p *stubProvider) Models(context.Context) ([]provider.ModelInfo, error) { return nil, nil }

// stubSource is a ProviderSource over a set of stub providers.
type stubSource struct{ providers map[string]*stubProvider }

func (s stubSource) ListProviders() []string {
	names := make([]string, 0, len(s.providers))
	for n := range s.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (s stubSource) GetProvider(name string) (provider.LLMProvider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, provider.ErrProviderNotFound
	}
	return p, nil
}

// eventSink collects health events for assertions.
type eventSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *eventSink) listen(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}
func (s *eventSink) has(t EventType) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Type == t {
			return true
		}
	}
	return false
}

func newMonitorFixture(t *testing.T, cfg Config, clk *fakeClock, providers map[string]*stubProvider) (*Monitor, *Manager, *Registry, *eventSink) {
	t.Helper()
	breakers := NewManager(cfg, WithManagerClock(clk.Now))
	reg := NewRegistry()
	sink := &eventSink{}
	mon := NewMonitor(MonitorConfig{Interval: time.Second, Timeout: time.Second},
		stubSource{providers: providers}, breakers, reg,
		WithMonitorClock(clk.Now), WithListener(sink.listen))
	return mon, breakers, reg, sink
}

func TestMonitor_HealthyProvider(t *testing.T) {
	clk := newClock()
	p := &stubProvider{name: "p"}
	p.healthy.Store(true)
	mon, breakers, reg, sink := newMonitorFixture(t, Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: 10 * time.Second}, clk, map[string]*stubProvider{"p": p})

	mon.CheckNow(context.Background())

	if breakers.State("p") != StateClosed {
		t.Errorf("state = %s, want closed", breakers.State("p"))
	}
	rec, ok := reg.Record("p")
	if !ok || rec.State != StateClosed || !rec.Available || rec.LastSuccess.IsZero() {
		t.Errorf("record = %+v", rec)
	}
	if sink.has(EventProviderDown) {
		t.Errorf("healthy provider emitted ProviderDown")
	}
}

func TestMonitor_UnhealthyProvider(t *testing.T) {
	clk := newClock()
	p := &stubProvider{name: "p"} // healthy=false
	mon, breakers, reg, sink := newMonitorFixture(t, Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: 10 * time.Second}, clk, map[string]*stubProvider{"p": p})
	ctx := context.Background()

	mon.CheckNow(ctx) // fail 1
	if breakers.State("p") != StateClosed {
		t.Fatalf("after 1 failure state = %s, want closed", breakers.State("p"))
	}
	mon.CheckNow(ctx) // fail 2 -> open
	if breakers.State("p") != StateOpen {
		t.Errorf("after threshold state = %s, want open", breakers.State("p"))
	}
	rec, _ := reg.Record("p")
	if rec.State != StateOpen || rec.Available || rec.LastFailure.IsZero() || rec.LastError == "" {
		t.Errorf("record = %+v", rec)
	}
	if !sink.has(EventProviderDown) || !sink.has(EventStateChanged) {
		t.Errorf("expected ProviderDown + StateChanged events")
	}
}

func TestMonitor_Recovery(t *testing.T) {
	clk := newClock()
	p := &stubProvider{name: "p"} // starts unhealthy
	mon, breakers, reg, sink := newMonitorFixture(t, Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: 10 * time.Second, HalfOpenMaxRequests: 1}, clk, map[string]*stubProvider{"p": p})
	ctx := context.Background()

	mon.CheckNow(ctx)
	mon.CheckNow(ctx) // -> open
	if breakers.State("p") != StateOpen {
		t.Fatalf("precondition: want open, got %s", breakers.State("p"))
	}

	// Provider recovers, but the breaker is still cooling: the probe is gated and
	// the provider is NOT contacted.
	p.healthy.Store(true)
	before := p.checks.Load()
	mon.CheckNow(ctx)
	if p.checks.Load() != before {
		t.Errorf("provider was probed while circuit open/cooling")
	}
	if breakers.State("p") != StateOpen {
		t.Errorf("state changed during cooldown: %s", breakers.State("p"))
	}

	// After the cooldown, the probe is admitted (half-open) and its success closes
	// the breaker -> automatic recovery.
	clk.Advance(11 * time.Second)
	mon.CheckNow(ctx)
	if breakers.State("p") != StateClosed {
		t.Errorf("after recovery state = %s, want closed", breakers.State("p"))
	}
	rec, _ := reg.Record("p")
	if rec.State != StateClosed || !rec.Available {
		t.Errorf("recovered record = %+v", rec)
	}
	if !sink.has(EventProviderRecovered) {
		t.Errorf("expected ProviderRecovered event")
	}
}

func TestMonitor_ProbeTransportFailure(t *testing.T) {
	clk := newClock()
	p := &stubProvider{name: "p"}
	p.healthy.Store(true)
	p.transportErr.Store(true) // HealthCheck returns an error
	mon, breakers, reg, _ := newMonitorFixture(t, Config{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: 10 * time.Second}, clk, map[string]*stubProvider{"p": p})

	mon.CheckNow(context.Background())
	if breakers.State("p") != StateOpen {
		t.Errorf("transport failure did not trip breaker: %s", breakers.State("p"))
	}
	rec, _ := reg.Record("p")
	if rec.LastError == "" {
		t.Errorf("record missing LastError")
	}
}

func TestMonitor_Timing(t *testing.T) {
	// Real clock: probe latency reflects the health check duration.
	p := &stubProvider{name: "p", sleep: 20 * time.Millisecond}
	p.healthy.Store(true)
	breakers := NewManager(DefaultConfig())
	reg := NewRegistry()
	mon := NewMonitor(MonitorConfig{Interval: time.Hour, Timeout: time.Second}, stubSource{providers: map[string]*stubProvider{"p": p}}, breakers, reg)

	mon.CheckNow(context.Background())
	rec, _ := reg.Record("p")
	if rec.Latency < 20*time.Millisecond {
		t.Errorf("measured latency = %s, want >= 20ms", rec.Latency)
	}
}

func TestMonitor_ConcurrentMonitoring(t *testing.T) {
	providers := map[string]*stubProvider{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		p := &stubProvider{name: n}
		p.healthy.Store(n != "c") // c is unhealthy
		providers[n] = p
	}
	breakers := NewManager(Config{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Millisecond})
	reg := NewRegistry()
	mon := NewMonitor(MonitorConfig{Interval: time.Hour, Timeout: time.Second}, stubSource{providers: providers}, breakers, reg)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); mon.CheckNow(context.Background()) }()
	}
	wg.Wait()

	if len(reg.Records()) != 5 {
		t.Errorf("records = %d, want 5", len(reg.Records()))
	}
}

func TestMonitor_StartStop(t *testing.T) {
	p := &stubProvider{name: "p"}
	p.healthy.Store(true)
	breakers := NewManager(DefaultConfig())
	reg := NewRegistry()
	mon := NewMonitor(MonitorConfig{Interval: 5 * time.Millisecond, Timeout: time.Second}, stubSource{providers: map[string]*stubProvider{"p": p}}, breakers, reg)

	mon.Start(context.Background())
	mon.Start(context.Background()) // idempotent

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := reg.Record("p"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("monitor did not probe within deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mon.Stop()
	mon.Stop() // idempotent, must not hang
}
