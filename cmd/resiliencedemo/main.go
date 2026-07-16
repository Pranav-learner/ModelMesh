// Command resiliencedemo demonstrates the complete resilience layer end to end,
// fully offline: circuit breaking, health monitoring, automatic failover, and
// automatic recovery.
//
// Scenario:
//
//	Provider healthy   -> requests succeed on the primary
//	Kill provider      -> circuit opens -> router skips it -> traffic shifts to backup
//	Restore provider   -> health probe succeeds -> circuit closes -> traffic resumes
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// demoProvider is a controllable provider whose availability can be toggled.
type demoProvider struct {
	name string
	up   atomicBool
}

func (p *demoProvider) Name() string { return p.name }
func (p *demoProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	if !p.up.get() {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", provider.ErrProviderUnavailable)
	}
	return provider.ChatResponse{
		Provider: p.name, Model: "mock-chat",
		Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "ok"}}},
		Usage:   provider.Usage{TotalTokens: 1},
	}, nil
}
func (p *demoProvider) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (p *demoProvider) Models(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "mock-chat", Capabilities: []provider.Capability{provider.CapabilityChat}}}, nil
}
func (p *demoProvider) HealthCheck(context.Context) (provider.HealthStatus, error) {
	if p.up.get() {
		return provider.HealthStatus{State: provider.HealthStateHealthy}, nil
	}
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) set(v bool) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicBool) get() bool  { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// demoClock is a manually-advanced clock.
type demoClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *demoClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *demoClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	clk := &demoClock{t: time.Unix(1_000_000, 0)}

	primary := &demoProvider{name: "primary"}
	primary.up.set(true)
	backup := &demoProvider{name: "backup"}
	backup.up.set(true)

	reg := provider.NewRegistry()
	_ = reg.Register(primary)
	_ = reg.Register(backup)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("primary"))

	healthReg := resilience.NewRegistry()

	rcfg := routing.DefaultConfig()
	rcfg.Weighted.Factors = routing.FactorWeights{Quality: 0.5, Availability: 0.5}
	rcfg.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{"primary": 0.99, "backup": 0.90}}
	strat := routing.NewWeighted(rcfg.Weighted, routing.WithHealthProvider(healthReg))
	router := routing.NewManager(pm, strat, rcfg)

	breakers := resilience.NewManager(
		resilience.Config{FailureThreshold: 2, SuccessThreshold: 1, OpenTimeout: 5 * time.Second, HalfOpenMaxRequests: 1},
		resilience.WithManagerClock(clk.Now),
	)
	failover := resilience.NewFailover(breakers)
	monitor := resilience.NewMonitor(resilience.MonitorConfig{Interval: time.Hour, Timeout: time.Second}, pm, breakers, healthReg, resilience.WithMonitorClock(clk.Now))

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false}, gateway.WithFailover(failover, pm))

	send := func(label string, n int) {
		counts := map[string]int{}
		for i := 0; i < n; i++ {
			res, err := gw.Chat(ctx, req())
			if err != nil {
				counts["ERROR"]++
				continue
			}
			counts[res.Response.Provider]++
		}
		fmt.Printf("  %-22s served: %v   breakers: [%s]\n", label, counts, breakers.ExplainStates())
	}

	fmt.Println("=== Resilience demo ===")
	fmt.Println("\n1. Both providers healthy:")
	send("5 requests", 5)

	fmt.Println("\n2. Kill the primary provider:")
	primary.up.set(false)
	send("request 1", 1)       // primary fails -> failover to backup
	send("request 2", 1)       // primary fails again -> breaker opens
	send("3 more requests", 3) // primary skipped (open) -> backup

	fmt.Println("\n3. Restore the primary and let the monitor probe after cooldown:")
	primary.up.set(true)
	clk.advance(6 * time.Second) // past OpenTimeout
	monitor.CheckNow(ctx)        // half-open probe succeeds -> breaker closes
	fmt.Printf("  monitor probe done         breakers: [%s]\n", breakers.ExplainStates())
	send("5 requests", 5) // traffic resumes to the recovered primary

	return nil
}

func req() provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}}
}
