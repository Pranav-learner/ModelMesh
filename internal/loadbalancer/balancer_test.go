package loadbalancer_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lb "github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func newInst(id, prov, region string) lb.Instance {
	return lb.Instance{ID: id, Provider: prov, Region: region}
}

func mustRegister(t *testing.T, b *lb.Balancer, instances ...lb.Instance) {
	t.Helper()
	for _, i := range instances {
		if err := b.Register(i); err != nil {
			t.Fatalf("register %s: %v", i.ID, err)
		}
	}
}

// --- Round Robin ---

func TestBalancer_RoundRobinEvenDistribution(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyRoundRobin}, lb.NewRoundRobin())
	mustRegister(t, b, newInst("a", "openai", "us-east-1"), newInst("b", "openai", "eu-west-1"), newInst("c", "openai", "us-west-2"))

	counts := map[string]int{}
	for i := 0; i < 30; i++ {
		sel, err := b.Select(context.Background(), lb.Request{})
		if err != nil {
			t.Fatalf("select %d: %v", i, err)
		}
		counts[sel.Instance.ID]++
	}
	for _, id := range []string{"a", "b", "c"} {
		if counts[id] != 10 {
			t.Errorf("instance %s selected %d times, want 10 (even)", id, counts[id])
		}
	}
}

// --- Least Latency ---

func TestBalancer_LeastLatencyRoutesToFastest(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyLeastLatency}, lb.NewLeastLatency())
	mustRegister(t, b, newInst("slow", "openai", ""), newInst("fast", "openai", ""), newInst("mid", "openai", ""))

	// Feed measured latencies so none are "unmeasured".
	_ = b.Update(lb.Observation{InstanceID: "slow", Latency: 200 * time.Millisecond})
	_ = b.Update(lb.Observation{InstanceID: "fast", Latency: 15 * time.Millisecond})
	_ = b.Update(lb.Observation{InstanceID: "mid", Latency: 80 * time.Millisecond})

	for i := 0; i < 5; i++ {
		sel, err := b.Select(context.Background(), lb.Request{})
		if err != nil {
			t.Fatalf("select %d: %v", i, err)
		}
		if sel.Instance.ID != "fast" {
			t.Fatalf("select %d chose %q, want fast", i, sel.Instance.ID)
		}
	}
}

func TestBalancer_LeastLatencyAdaptsToNewMeasurements(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyLeastLatency, LatencyWindow: 3}, lb.NewLeastLatency())
	mustRegister(t, b, newInst("a", "openai", ""), newInst("b", "openai", ""))

	_ = b.Update(lb.Observation{InstanceID: "a", Latency: 10 * time.Millisecond})
	_ = b.Update(lb.Observation{InstanceID: "b", Latency: 100 * time.Millisecond})
	if sel, _ := b.Select(context.Background(), lb.Request{}); sel.Instance.ID != "a" {
		t.Fatalf("initially chose %q, want a", sel.Instance.ID)
	}

	// a degrades sharply; window size 3 lets the new samples dominate.
	for i := 0; i < 3; i++ {
		_ = b.Update(lb.Observation{InstanceID: "a", Latency: 300 * time.Millisecond})
	}
	if sel, _ := b.Select(context.Background(), lb.Request{}); sel.Instance.ID != "b" {
		t.Errorf("after a degraded, chose %q, want b", sel.Instance.ID)
	}
}

// --- Instance Registration ---

func TestBalancer_Registration(t *testing.T) {
	b := lb.New(lb.DefaultConfig(), lb.NewRoundRobin())

	if err := b.Register(newInst("a", "openai", "")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := b.Register(newInst("a", "openai", "")); !errors.Is(err, lb.ErrInstanceExists) {
		t.Errorf("dup = %v, want ErrInstanceExists", err)
	}
	if err := b.Register(lb.Instance{ID: "x"}); !errors.Is(err, lb.ErrInvalidInstance) {
		t.Errorf("invalid = %v, want ErrInvalidInstance", err)
	}
	if err := b.Remove("a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := b.Remove("a"); !errors.Is(err, lb.ErrInstanceNotFound) {
		t.Errorf("remove missing = %v, want ErrInstanceNotFound", err)
	}
	if _, err := b.Select(context.Background(), lb.Request{}); !errors.Is(err, lb.ErrNoInstances) {
		t.Errorf("select empty = %v, want ErrNoInstances", err)
	}
}

// --- Disabled Instance ---

func TestBalancer_DisabledInstanceSkipped(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyRoundRobin}, lb.NewRoundRobin())
	mustRegister(t, b, newInst("a", "openai", ""), newInst("b", "openai", ""))

	if err := b.Disable("a"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		sel, err := b.Select(context.Background(), lb.Request{})
		if err != nil {
			t.Fatalf("select %d: %v", i, err)
		}
		if sel.Instance.ID == "a" {
			t.Fatalf("disabled instance a was selected")
		}
	}

	// Re-enabling brings it back into rotation.
	if err := b.Enable("a"); err != nil {
		t.Fatal(err)
	}
	seenA := false
	for i := 0; i < 10; i++ {
		sel, _ := b.Select(context.Background(), lb.Request{})
		if sel.Instance.ID == "a" {
			seenA = true
		}
	}
	if !seenA {
		t.Errorf("re-enabled instance a never selected")
	}
}

func TestBalancer_AllDisabledYieldsNoInstances(t *testing.T) {
	b := lb.New(lb.DefaultConfig(), lb.NewRoundRobin())
	mustRegister(t, b, newInst("a", "openai", ""))
	_ = b.Disable("a")
	if _, err := b.Select(context.Background(), lb.Request{}); !errors.Is(err, lb.ErrNoInstances) {
		t.Errorf("all disabled = %v, want ErrNoInstances", err)
	}
}

// --- Health gating (Resilience integration) ---

type fakeHealth map[string]provider.HealthState

func (f fakeHealth) Health(name string) (provider.HealthStatus, bool) {
	st, ok := f[name]
	if !ok {
		return provider.HealthStatus{}, false
	}
	return provider.HealthStatus{Provider: name, State: st}, true
}

func TestBalancer_UnhealthyProviderGated(t *testing.T) {
	health := fakeHealth{"openai": provider.HealthStateUnhealthy, "anthropic": provider.HealthStateHealthy}
	b := lb.New(lb.Config{Strategy: lb.StrategyRoundRobin}, lb.NewRoundRobin(), lb.WithHealthSource(health))
	mustRegister(t, b, newInst("oa", "openai", ""), newInst("an", "anthropic", ""))

	for i := 0; i < 6; i++ {
		sel, err := b.Select(context.Background(), lb.Request{})
		if err != nil {
			t.Fatalf("select %d: %v", i, err)
		}
		if sel.Instance.Provider != "anthropic" {
			t.Fatalf("selected unhealthy provider %q", sel.Instance.Provider)
		}
	}
}

// --- Provider filter ---

func TestBalancer_ProviderFilter(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyRoundRobin}, lb.NewRoundRobin())
	mustRegister(t, b, newInst("oa1", "openai", ""), newInst("an1", "anthropic", ""))

	for i := 0; i < 4; i++ {
		sel, err := b.Select(context.Background(), lb.Request{Provider: "anthropic"})
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if sel.Instance.Provider != "anthropic" {
			t.Fatalf("provider filter returned %q", sel.Instance.Provider)
		}
	}
}

// --- Concurrent Selection ---

func TestBalancer_ConcurrentSelection(t *testing.T) {
	b := lb.New(lb.Config{Strategy: lb.StrategyLeastLatency, LatencyWindow: 10}, lb.NewLeastLatency())
	mustRegister(t, b, newInst("a", "openai", ""), newInst("b", "openai", ""), newInst("c", "openai", ""))

	const workers, perWorker = 16, 100
	var wg sync.WaitGroup
	var selected atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				sel, err := b.Select(context.Background(), lb.Request{})
				if err != nil {
					t.Errorf("concurrent select: %v", err)
					return
				}
				selected.Add(1)
				_ = b.Update(lb.Observation{InstanceID: sel.Instance.ID, Latency: 20 * time.Millisecond, Success: true})
			}
		}()
	}
	wg.Wait()

	total := int64(workers * perWorker)
	if selected.Load() != total {
		t.Fatalf("selected %d, want %d", selected.Load(), total)
	}
	stats := b.Statistics()
	if stats.TotalSelections != uint64(total) {
		t.Errorf("TotalSelections = %d, want %d", stats.TotalSelections, total)
	}
	var sum uint64
	for _, s := range stats.Instances {
		sum += s.RequestCount
	}
	if sum != uint64(total) {
		t.Errorf("sum of per-instance RequestCount = %d, want %d", sum, total)
	}
}

// --- Statistics ---

func TestBalancer_Statistics(t *testing.T) {
	clk := int64(0)
	now := func() time.Time { return time.Unix(0, atomic.LoadInt64(&clk)) }
	b := lb.New(lb.Config{Strategy: lb.StrategyRoundRobin}, lb.NewRoundRobin(), lb.WithClock(now))
	mustRegister(t, b, newInst("a", "openai", "us-east-1"), newInst("b", "openai", "eu-west-1"), newInst("c", "anthropic", ""))
	_ = b.Disable("c")

	atomic.StoreInt64(&clk, int64(time.Second))
	for i := 0; i < 4; i++ {
		if _, err := b.Select(context.Background(), lb.Request{Provider: "openai"}); err != nil {
			t.Fatalf("select: %v", err)
		}
	}
	_ = b.Update(lb.Observation{InstanceID: "a", Latency: 30 * time.Millisecond})

	stats := b.Statistics()
	if stats.Strategy != lb.StrategyRoundRobin {
		t.Errorf("Strategy = %q", stats.Strategy)
	}
	if stats.TotalInstances != 3 || stats.EnabledCount != 2 || stats.HealthyCount != 2 {
		t.Errorf("pool counts: total=%d enabled=%d healthy=%d, want 3/2/2", stats.TotalInstances, stats.EnabledCount, stats.HealthyCount)
	}
	if stats.TotalSelections != 4 {
		t.Errorf("TotalSelections = %d, want 4", stats.TotalSelections)
	}

	byID := map[string]lb.InstanceStats{}
	for _, s := range stats.Instances {
		byID[s.ID] = s
	}
	if byID["a"].RequestCount+byID["b"].RequestCount != 4 {
		t.Errorf("openai request counts sum = %d, want 4", byID["a"].RequestCount+byID["b"].RequestCount)
	}
	if byID["a"].AverageLatency != 30*time.Millisecond {
		t.Errorf("a AverageLatency = %v, want 30ms", byID["a"].AverageLatency)
	}
	if byID["a"].LastUsed != time.Unix(0, int64(time.Second)) {
		t.Errorf("a LastUsed = %v, want stamped clock", byID["a"].LastUsed)
	}
	if byID["c"].Healthy {
		t.Errorf("disabled instance c reported healthy")
	}
	if byID["c"].RequestCount != 0 {
		t.Errorf("disabled instance c got %d requests", byID["c"].RequestCount)
	}
}

// --- Build from config ---

func TestBuild_FromConfig(t *testing.T) {
	b, err := lb.Build(lb.Config{Strategy: lb.StrategyLeastLatency})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if b.Strategy() != lb.StrategyLeastLatency {
		t.Errorf("strategy = %q", b.Strategy())
	}
	if _, err := lb.Build(lb.Config{Strategy: "nope"}); !errors.Is(err, lb.ErrUnknownStrategy) {
		t.Errorf("unknown = %v, want ErrUnknownStrategy", err)
	}
	if _, err := lb.Build(lb.Config{Strategy: lb.StrategyRandom}); !errors.Is(err, lb.ErrStrategyNotImplemented) {
		t.Errorf("reserved = %v, want ErrStrategyNotImplemented", err)
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := lb.DefaultConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
	if err := (lb.Config{Strategy: lb.StrategyRoundRobin, LatencyWindow: -1}).Validate(); !errors.Is(err, lb.ErrInvalidConfig) {
		t.Errorf("negative window = %v, want ErrInvalidConfig", err)
	}
	if err := (lb.Config{Strategy: "", LatencyWindow: 5}).Validate(); !errors.Is(err, lb.ErrInvalidConfig) {
		t.Errorf("empty strategy = %v, want ErrInvalidConfig", err)
	}
}
