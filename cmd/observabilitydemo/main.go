// Command observabilitydemo exercises ModelMesh's full observability platform end
// to end, fully offline. It wires the gateway with metrics, tracing, correlated
// logging, health monitoring, and failover, fires 100 requests through it, and
// then shows every observability surface:
//
//   - Prometheus metrics (rendered via observability.InspectMetrics)
//   - the distributed trace tree of a single request (observability.InspectTrace)
//   - live provider health (observability.InspectHealth)
//   - a failover explanation (observability.ExplainFailover)
//   - sample correlated structured logs
//
// It also serves the live metrics endpoint on :2112/metrics so a real Prometheus
// (deploy/docker-compose.yml) can scrape it and the Grafana dashboards light up.
//
// Usage:
//
//	go run ./cmd/observabilitydemo          # fire 100 requests, print report, exit
//	go run ./cmd/observabilitydemo -serve   # also keep /metrics up for scraping
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

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

// demoProvider is a controllable offline provider.
type demoProvider struct {
	name string
	up   bool
}

func (p *demoProvider) Name() string { return p.name }
func (p *demoProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	if !p.up {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", provider.ErrProviderUnavailable)
	}
	return provider.ChatResponse{
		ID: "r", Provider: p.name, Model: "mock-chat",
		Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "hello from " + p.name}, FinishReason: provider.FinishReasonStop}},
		Usage:   provider.Usage{TotalTokens: 42},
	}, nil
}
func (p *demoProvider) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (p *demoProvider) Models(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "mock-chat", Capabilities: []provider.Capability{provider.CapabilityChat}}}, nil
}
func (p *demoProvider) HealthCheck(context.Context) (provider.HealthStatus, error) {
	if p.up {
		return provider.HealthStatus{State: provider.HealthStateHealthy}, nil
	}
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}

func main() {
	serve := flag.Bool("serve", false, "keep serving /metrics on :2112 after the run")
	flag.Parse()

	ctx := context.Background()

	// --- Observability plane ---
	mgr := metrics.NewManager()
	met := metrics.New(mgr)

	exporter := tracetest.NewInMemoryExporter()
	tp, err := tracing.NewProvider(tracing.WithServiceName("modelmesh"), tracing.WithSyncExporter(exporter))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracing:", err)
		os.Exit(1)
	}
	defer tp.Shutdown(ctx)

	logs := &bytes.Buffer{}
	log := logger.NewWithWriter(logs, logger.LevelInfo)

	// --- Providers: primary is down so failover + breaker are exercised. ---
	primary := &demoProvider{name: "openai", up: false}
	backup := &demoProvider{name: "anthropic", up: true}

	reg := provider.NewRegistry()
	_ = reg.Register(primary)
	_ = reg.Register(backup)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	healthReg := resilience.NewRegistry()
	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Quality: 0.5, Availability: 0.5}
	rcfg.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{"openai": 0.99, "anthropic": 0.9}}
	strat := routing.NewWeighted(rcfg.Weighted, routing.WithHealthProvider(healthReg))
	router := routing.NewManager(pm, strat, rcfg)

	breakers := resilience.NewManager(resilience.Config{FailureThreshold: 3, SuccessThreshold: 1, OpenTimeout: 5 * time.Second, HalfOpenMaxRequests: 1})
	failover := resilience.NewFailover(breakers)

	monitor := resilience.NewMonitor(resilience.MonitorConfig{Interval: time.Hour, Timeout: time.Second}, pm, breakers, healthReg)
	monitor.AddListener(observability.BreakerListener(met))

	l1 := cache.NewMemoryCache(cache.DefaultConfig().Memory)
	cm := cache.NewManager([]cache.Cache{l1})

	gw := gateway.New(router, cm, cache.DefaultConfig(),
		gateway.WithFailover(failover, pm),
		gateway.WithMetrics(met),
		gateway.WithTracer(tp.Tracer("gateway")),
		gateway.WithLogger(log),
		gateway.WithCostEstimator(func(_ string, u provider.Usage) float64 {
			return float64(u.TotalTokens) * 0.000002 // ~$2 / 1M tokens
		}),
	)

	// --- Fire 100 requests concurrently. First request warms the cache, so most
	// of the rest are served from L1 (demonstrating cache + cost-saved metrics). ---
	fmt.Println("Firing 100 requests through the gateway...")
	const n = 100
	req := provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "What is ModelMesh?"}}}
	if _, err := gw.Chat(ctx, req); err != nil {
		fmt.Fprintln(os.Stderr, "warmup:", err)
		os.Exit(1)
	}
	var wg sync.WaitGroup
	var failCount int64
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := gw.Chat(ctx, req); err != nil {
				mu.Lock()
				failCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Refresh health + publish gauges from the breaker snapshot.
	monitor.CheckNow(ctx)
	observability.Publish(met, breakers)

	// --- Report ---
	fmt.Printf("\nCompleted %d requests (%d errors)\n", n+1, failCount)

	section("PROMETHEUS METRICS  (observability.InspectMetrics)")
	dump, _ := observability.InspectMetrics(mgr)
	fmt.Print(dump)

	section("DISTRIBUTED TRACE  (observability.InspectTrace — one representative request)")
	fmt.Print(firstTraceTree(exporter))

	section("PROVIDER HEALTH  (observability.InspectHealth)")
	fmt.Print(observability.InspectHealth(healthReg))

	section("FAILOVER EXPLANATION  (observability.ExplainFailover)")
	if outcome, err := singleTracedRequest(ctx, gw); err == nil && outcome != nil {
		fmt.Println(observability.ExplainFailover(*outcome))
	}

	section("STRUCTURED LOGS  (correlated: request_id / trace_id)")
	printLogSample(logs, 3)

	if *serve {
		http.Handle("/metrics", mgr.Handler())
		fmt.Println("\nServing metrics on http://localhost:2112/metrics (Ctrl-C to stop)")
		fmt.Println("Start the stack with: docker compose -f deploy/docker-compose.yml up -d")
		if err := http.ListenAndServe(":2112", nil); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
	}
}

func singleTracedRequest(ctx context.Context, gw *gateway.Engine) (*resilience.FailoverOutcome, error) {
	res, err := gw.Chat(ctx, provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "explain failover"}}})
	if err != nil {
		return nil, err
	}
	return res.Failover, nil
}

func section(title string) {
	fmt.Printf("\n%s\n%s\n", title, strings.Repeat("=", len(title)))
}

// firstTraceTree renders only one request's span tree so the demo output stays
// readable (the in-memory exporter holds all 100+ requests' spans). It keeps the
// spans belonging to the first root span's trace.
func firstTraceTree(exp *tracetest.InMemoryExporter) string {
	all := exp.GetSpans()
	if len(all) == 0 {
		return "(no spans captured)\n"
	}
	var traceID trace.TraceID
	for _, s := range all {
		if !s.Parent.IsValid() {
			traceID = s.SpanContext.TraceID()
			break
		}
	}
	one := tracetest.SpanStubs{}
	for _, s := range all {
		if s.SpanContext.TraceID() == traceID {
			one = append(one, s)
		}
	}
	sub := tracetest.NewInMemoryExporter()
	_ = sub.ExportSpans(context.Background(), one.Snapshots())
	return observability.InspectTrace(sub)
}

func printLogSample(logs *bytes.Buffer, n int) {
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	start := 0
	if len(lines) > n {
		start = len(lines) - n // most recent entries carry request/trace IDs
	}
	for _, l := range lines[start:] {
		fmt.Println(l)
	}
}
