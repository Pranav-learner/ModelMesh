package observability_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/metrics"
	"github.com/symbiotes/modelmesh/internal/observability"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

// flaky is a controllable provider for the integration wiring.
type flaky struct {
	name string
	up   bool
}

func (p *flaky) Name() string { return p.name }
func (p *flaky) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	if !p.up {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", provider.ErrProviderUnavailable)
	}
	return provider.ChatResponse{
		ID: "r", Provider: p.name, Model: "mock-chat",
		Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok"}, FinishReason: provider.FinishReasonStop}},
		Usage:   provider.Usage{TotalTokens: 10},
	}, nil
}
func (p *flaky) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (p *flaky) Models(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "mock-chat", Capabilities: []provider.Capability{provider.CapabilityChat}}}, nil
}
func (p *flaky) HealthCheck(context.Context) (provider.HealthStatus, error) {
	if p.up {
		return provider.HealthStatus{State: provider.HealthStateHealthy}, nil
	}
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}

// stack bundles a fully-wired, fully-observed gateway for the integration tests.
type stack struct {
	gw       *gateway.Engine
	mgr      *metrics.Manager
	met      *metrics.Metrics
	exporter *tracetest.InMemoryExporter
	logs     *bytes.Buffer
	breakers *resilience.Manager
	registry *resilience.Registry
}

func newStack(t *testing.T, cacheOn bool, providers ...*flaky) *stack {
	t.Helper()

	mgr := metrics.NewManager()
	met := metrics.New(mgr)

	exporter := tracetest.NewInMemoryExporter()
	tp, err := tracing.NewProvider(tracing.WithServiceName("modelmesh-test"), tracing.WithSyncExporter(exporter))
	if err != nil {
		t.Fatalf("tracing provider: %v", err)
	}
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	logs := &bytes.Buffer{}
	log := logger.NewWithWriter(logs, logger.LevelInfo)

	reg := provider.NewRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatalf("register %s: %v", p.name, err)
		}
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider(providers[0].name))

	healthReg := resilience.NewRegistry()
	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Quality: 0.5, Availability: 0.5}
	qualities := map[string]float64{}
	for i, p := range providers {
		qualities[p.name] = 0.99 - float64(i)*0.05
	}
	rcfg.Weighted.Quality = routing.QualityConfig{Providers: qualities}
	strat := routing.NewWeighted(rcfg.Weighted, routing.WithHealthProvider(healthReg))
	router := routing.NewManager(pm, strat, rcfg)

	breakers := resilience.NewManager(resilience.Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: time.Second, HalfOpenMaxRequests: 1})
	failover := resilience.NewFailover(breakers)

	var c gateway.Cache
	var cfg cache.Config
	if cacheOn {
		l1 := cache.NewMemoryCache(cache.DefaultConfig().Memory)
		c = cache.NewManager([]cache.Cache{l1})
		cfg = cache.DefaultConfig()
	} else {
		c = cache.NewManager(nil)
		cfg = cache.Config{Enabled: false}
	}

	gw := gateway.New(router, c, cfg,
		gateway.WithFailover(failover, pm),
		gateway.WithMetrics(met),
		gateway.WithTracer(tp.Tracer("gateway")),
		gateway.WithLogger(log),
		gateway.WithCostEstimator(func(_ string, u provider.Usage) float64 {
			return float64(u.TotalTokens) * 0.00001
		}),
	)
	return &stack{gw: gw, mgr: mgr, met: met, exporter: exporter, logs: logs, breakers: breakers, registry: healthReg}
}

func doChat(ctx context.Context, gw *gateway.Engine) (*gateway.ChatResult, error) {
	return gw.Chat(ctx, provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "same prompt"}}})
}

// counterValue reads a single counter/gauge family's summed value from the registry.
func metricValue(t *testing.T, mgr *metrics.Manager, name string) float64 {
	t.Helper()
	families, err := mgr.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			switch {
			case m.Counter != nil:
				total += m.GetCounter().GetValue()
			case m.Gauge != nil:
				total += m.GetGauge().GetValue()
			case m.Histogram != nil:
				total += float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	return total
}

// TestIntegration_MetricsTracingLogsConcurrent fires many concurrent requests
// through the fully-wired gateway and validates that metrics, traces, and
// correlated logs are all produced.
func TestIntegration_MetricsTracingLogsConcurrent(t *testing.T) {
	s := newStack(t, true, &flaky{name: "primary", up: true}, &flaky{name: "backup", up: true})
	ctx := context.Background()

	// Warm the cache so the concurrent burst is served from L1.
	if _, err := doChat(ctx, s.gw); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := doChat(ctx, s.gw); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent chat: %v", err)
	}

	total := n + 1 // + warmup

	// --- Metrics ---
	if got := metricValue(t, s.mgr, "modelmesh_gateway_requests_total"); got != float64(total) {
		t.Errorf("gateway_requests_total = %v, want %d", got, total)
	}
	if got := metricValue(t, s.mgr, "modelmesh_gateway_request_duration_seconds"); got != float64(total) {
		t.Errorf("gateway_request_duration_seconds count = %v, want %d", got, total)
	}
	hits := metricValue(t, s.mgr, "modelmesh_cache_hits_total")
	misses := metricValue(t, s.mgr, "modelmesh_cache_misses_total")
	if hits+misses != float64(total) {
		t.Errorf("hits(%v)+misses(%v) = %v, want %d", hits, misses, hits+misses, total)
	}
	if hits == 0 {
		t.Errorf("expected cache hits after warmup, got 0")
	}
	if metricValue(t, s.mgr, "modelmesh_provider_requests_total") == 0 {
		t.Errorf("expected at least one provider request")
	}
	if metricValue(t, s.mgr, "modelmesh_cache_tokens_saved_total") == 0 {
		t.Errorf("expected tokens saved > 0")
	}
	if metricValue(t, s.mgr, "modelmesh_cache_cost_saved_usd_total") == 0 {
		t.Errorf("expected cost saved > 0")
	}

	// --- Tracing ---
	spans := s.exporter.GetSpans()
	var roots, routes, lookups int
	for _, sp := range spans {
		switch sp.Name {
		case tracing.SpanRequest:
			roots++
		case tracing.SpanRoute:
			routes++
		case tracing.SpanCacheLookup:
			lookups++
		}
	}
	if roots != total {
		t.Errorf("root spans = %d, want %d", roots, total)
	}
	if routes == 0 || lookups == 0 {
		t.Errorf("expected route(%d) and cache.lookup(%d) child spans", routes, lookups)
	}

	// --- Logs correlated ---
	logOut := s.logs.String()
	if !strings.Contains(logOut, "request_id") || !strings.Contains(logOut, "trace_id") {
		t.Errorf("logs missing correlation IDs: %.200s", logOut)
	}

	// --- InspectMetrics renders ---
	dump, err := observability.InspectMetrics(s.mgr)
	if err != nil {
		t.Fatalf("InspectMetrics: %v", err)
	}
	if !strings.Contains(dump, "modelmesh_gateway_requests_total") {
		t.Errorf("InspectMetrics missing gateway metric:\n%s", dump)
	}

	// --- InspectTrace renders a tree ---
	tree := observability.InspectTrace(s.exporter)
	if !strings.Contains(tree, tracing.SpanRequest) {
		t.Errorf("InspectTrace missing root span:\n%.300s", tree)
	}
}

// TestIntegration_FailoverAndBreakerMetrics drives a provider outage and asserts
// the failover, error, and circuit-state metrics + gauges reflect it.
func TestIntegration_FailoverAndBreakerMetrics(t *testing.T) {
	s := newStack(t, false, &flaky{name: "primary", up: false}, &flaky{name: "backup", up: true})
	ctx := context.Background()

	const n = 5
	for i := 0; i < n; i++ {
		res, err := doChat(ctx, s.gw)
		if err != nil {
			t.Fatalf("chat %d: %v", i, err)
		}
		if res.Response.Provider != "backup" {
			t.Fatalf("chat %d served by %q, want backup", i, res.Response.Provider)
		}
	}

	if got := metricValue(t, s.mgr, "modelmesh_failovers_total"); got != float64(n) {
		t.Errorf("failovers_total = %v, want %d", got, n)
	}
	if metricValue(t, s.mgr, "modelmesh_provider_errors_total") == 0 {
		t.Errorf("expected provider errors from downed primary")
	}

	// Publish gauges from the breaker snapshot.
	observability.Publish(s.met, s.breakers)
	if metricValue(t, s.mgr, "modelmesh_circuit_open_circuits") == 0 {
		t.Errorf("expected primary breaker open after repeated failures")
	}
	if metricValue(t, s.mgr, "modelmesh_providers_unhealthy") == 0 {
		t.Errorf("expected at least one unhealthy provider")
	}
	if metricValue(t, s.mgr, "modelmesh_providers_healthy") == 0 {
		t.Errorf("expected backup to be healthy")
	}
}

// TestIntegration_BreakerListenerRecordsTransitions verifies the metrics bridge.
func TestIntegration_BreakerListenerRecordsTransitions(t *testing.T) {
	mgr := metrics.NewManager()
	met := metrics.New(mgr)
	listener := observability.BreakerListener(met)
	listener(resilience.Event{Type: resilience.EventStateChanged, Provider: "primary", From: resilience.StateClosed, To: resilience.StateOpen})

	if got := metricValue(t, mgr, "modelmesh_circuit_state_changes_total"); got != 1 {
		t.Errorf("circuit_state_changes_total = %v, want 1", got)
	}
}
