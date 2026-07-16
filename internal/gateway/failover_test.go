package gateway_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// flakyProvider is a controllable provider whose availability can be toggled.
type flakyProvider struct {
	name  string
	up    atomic.Bool
	chats atomic.Int32
}

func newFlaky(name string, up bool) *flakyProvider {
	p := &flakyProvider{name: name}
	p.up.Store(up)
	return p
}

func (p *flakyProvider) Name() string { return p.name }
func (p *flakyProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.chats.Add(1)
	if !p.up.Load() {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", provider.ErrProviderUnavailable)
	}
	return provider.ChatResponse{
		ID: "r", Provider: p.name, Model: "mock-chat",
		Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok from " + p.name}, FinishReason: provider.FinishReasonStop}},
		Usage:   provider.Usage{TotalTokens: 1},
	}, nil
}
func (p *flakyProvider) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (p *flakyProvider) Models(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "mock-chat", Capabilities: []provider.Capability{provider.CapabilityChat}}}, nil
}
func (p *flakyProvider) HealthCheck(context.Context) (provider.HealthStatus, error) {
	if p.up.Load() {
		return provider.HealthStatus{State: provider.HealthStateHealthy}, nil
	}
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}

// failoverFixture wires providers, health-aware routing, breakers, a failover
// executor, a health monitor, and a failover-enabled gateway.
type failoverFixture struct {
	gw       *gateway.Engine
	breakers *resilience.Manager
	registry *resilience.Registry
	monitor  *resilience.Monitor
	pm       *provider.Manager
}

func newFailoverFixture(t *testing.T, clk func() time.Time, providers ...*flakyProvider) *failoverFixture {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.name, err)
		}
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider(providers[0].name))

	healthReg := resilience.NewRegistry()

	// Health-aware routing: prefer higher quality, but availability (from the
	// health registry) can flip the order.
	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Quality: 0.5, Availability: 0.5}
	qualities := map[string]float64{}
	for i, p := range providers {
		qualities[p.name] = 0.99 - float64(i)*0.05 // first-listed provider is highest quality
	}
	rcfg.Weighted.Quality = routing.QualityConfig{Providers: qualities}
	strat := routing.NewWeighted(rcfg.Weighted, routing.WithHealthProvider(healthReg))
	router := routing.NewManager(pm, strat, rcfg)

	bmOpts := []resilience.ManagerOption{}
	if clk != nil {
		bmOpts = append(bmOpts, resilience.WithManagerClock(clk))
	}
	breakers := resilience.NewManager(resilience.Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: time.Second, HalfOpenMaxRequests: 1}, bmOpts...)

	failover := resilience.NewFailover(breakers)

	monOpts := []resilience.MonitorOption{}
	if clk != nil {
		monOpts = append(monOpts, resilience.WithMonitorClock(clk))
	}
	monitor := resilience.NewMonitor(resilience.MonitorConfig{Interval: time.Hour, Timeout: time.Second}, pm, breakers, healthReg, monOpts...)

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false}, gateway.WithFailover(failover, pm))
	return &failoverFixture{gw: gw, breakers: breakers, registry: healthReg, monitor: monitor, pm: pm}
}

func chat(ctx context.Context, gw *gateway.Engine) (*gateway.ChatResult, error) {
	return gw.Chat(ctx, provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}})
}

func TestFailover_HealthyPrimarySucceeds(t *testing.T) {
	f := newFailoverFixture(t, nil, newFlaky("primary", true), newFlaky("backup", true))
	res, err := chat(context.Background(), f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Response.Provider != "primary" || res.Failover.FailoverUsed {
		t.Errorf("served by %q (failover=%v), want primary without failover", res.Response.Provider, res.Failover.FailoverUsed)
	}
}

func TestFailover_ProviderOutageFailsOver(t *testing.T) {
	primary := newFlaky("primary", false) // down
	backup := newFlaky("backup", true)
	f := newFailoverFixture(t, nil, primary, backup)

	res, err := chat(context.Background(), f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Response.Provider != "backup" || !res.Failover.FailoverUsed {
		t.Errorf("served by %q (failover=%v), want backup with failover", res.Response.Provider, res.Failover.FailoverUsed)
	}
	if primary.chats.Load() == 0 {
		t.Errorf("primary should have been attempted before failover")
	}
	if res.Failover.Attempts[0].Err == nil {
		t.Errorf("first attempt should record the primary's failure")
	}
}

func TestFailover_BreakerSkipsAfterThreshold(t *testing.T) {
	primary := newFlaky("primary", false)
	backup := newFlaky("backup", true)
	f := newFailoverFixture(t, nil, primary, backup)
	ctx := context.Background()

	// Two failing requests trip the primary breaker (FailureThreshold=2).
	_, _ = chat(ctx, f.gw)
	_, _ = chat(ctx, f.gw)
	if f.breakers.State("primary") != resilience.StateOpen {
		t.Fatalf("primary breaker = %s, want open", f.breakers.State("primary"))
	}

	// The next request must SKIP primary (open circuit) — primary not contacted.
	before := primary.chats.Load()
	res, err := chat(ctx, f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if primary.chats.Load() != before {
		t.Errorf("primary was contacted despite open circuit")
	}
	if !res.Failover.Attempts[0].Skipped || res.Failover.Attempts[0].Reason != "circuit open" {
		t.Errorf("expected primary skipped for open circuit: %+v", res.Failover.Attempts[0])
	}
	if res.Response.Provider != "backup" {
		t.Errorf("served by %q, want backup", res.Response.Provider)
	}
}

func TestFailover_RecoveryResumesTraffic(t *testing.T) {
	clk := &clock{t: time.Unix(1_000_000, 0)}
	primary := newFlaky("primary", false)
	backup := newFlaky("backup", true)
	f := newFailoverFixture(t, clk.Now, primary, backup)
	ctx := context.Background()

	// Trip the primary breaker.
	_, _ = chat(ctx, f.gw)
	_, _ = chat(ctx, f.gw)
	if f.breakers.State("primary") != resilience.StateOpen {
		t.Fatalf("precondition: primary should be open")
	}

	// Restore the provider and let the monitor drive recovery after the cooldown.
	primary.up.Store(true)
	clk.Advance(2 * time.Second)
	f.monitor.CheckNow(ctx) // half-open probe succeeds -> close primary + mark healthy

	if f.breakers.State("primary") != resilience.StateClosed {
		t.Fatalf("primary breaker = %s, want closed after recovery", f.breakers.State("primary"))
	}

	// Traffic resumes to the recovered, higher-quality primary.
	res, err := chat(ctx, f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Response.Provider != "primary" {
		t.Errorf("served by %q, want primary (recovered)", res.Response.Provider)
	}
}

func TestFailover_RouterDownRanksUnhealthy(t *testing.T) {
	// With health-aware routing, an unhealthy primary is ranked BELOW the backup,
	// so the backup is tried first — router integration, before any dispatch.
	primary := newFlaky("primary", false)
	backup := newFlaky("backup", true)
	f := newFailoverFixture(t, nil, primary, backup)
	ctx := context.Background()

	// Probe until the primary's breaker trips (FailureThreshold=2), so the health
	// registry reports it unhealthy — the breaker debounces transient blips.
	f.monitor.CheckNow(ctx)
	f.monitor.CheckNow(ctx)
	if f.breakers.State("primary") != resilience.StateOpen {
		t.Fatalf("precondition: primary breaker should be open, got %s", f.breakers.State("primary"))
	}

	res, err := chat(ctx, f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Failover.Attempts[0].Target.Provider != "backup" {
		t.Errorf("first candidate = %q, want backup (unhealthy primary down-ranked)", res.Failover.Attempts[0].Target.Provider)
	}
	if res.Response.Provider != "backup" {
		t.Errorf("served by %q, want backup", res.Response.Provider)
	}
}

func TestFailover_MultipleProvidersAllButOneDown(t *testing.T) {
	a := newFlaky("a", false)
	b := newFlaky("b", false)
	c := newFlaky("c", true)
	f := newFailoverFixture(t, nil, a, b, c)

	res, err := chat(context.Background(), f.gw)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Response.Provider != "c" {
		t.Errorf("served by %q, want c (only healthy one)", res.Response.Provider)
	}
}

func TestFailover_AllDown(t *testing.T) {
	f := newFailoverFixture(t, nil, newFlaky("a", false), newFlaky("b", false))
	_, err := chat(context.Background(), f.gw)
	if !errors.Is(err, resilience.ErrAllProvidersFailed) {
		t.Fatalf("Chat() = %v, want ErrAllProvidersFailed", err)
	}
}

func TestFailover_ConcurrentFailures(t *testing.T) {
	primary := newFlaky("primary", false)
	backup := newFlaky("backup", true)
	f := newFailoverFixture(t, nil, primary, backup)
	ctx := context.Background()

	var wg sync.WaitGroup
	var served atomic.Int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res, err := chat(ctx, f.gw); err == nil && res.Response.Provider == "backup" {
				served.Add(1)
			}
		}()
	}
	wg.Wait()
	if served.Load() != 50 {
		t.Errorf("only %d/50 requests served by backup under concurrent failover", served.Load())
	}
}

// clock is a manually-advanced clock for the recovery test.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
