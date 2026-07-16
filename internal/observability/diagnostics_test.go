package observability_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/observability"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
)

func TestInspectHealth_RendersRecords(t *testing.T) {
	reg := provider.NewRegistry()
	up := &flaky{name: "primary", up: true}
	down := &flaky{name: "backup", up: false}
	if err := reg.Register(up); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(down); err != nil {
		t.Fatal(err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("primary"))

	breakers := resilience.NewManager(resilience.DefaultConfig())
	healthReg := resilience.NewRegistry()
	mon := resilience.NewMonitor(resilience.MonitorConfig{Interval: time.Hour, Timeout: time.Second}, pm, breakers, healthReg)
	mon.CheckNow(context.Background())

	out := observability.InspectHealth(healthReg)
	if !strings.Contains(out, "primary") || !strings.Contains(out, "backup") {
		t.Errorf("InspectHealth missing providers:\n%s", out)
	}
	if !strings.Contains(out, "state=") || !strings.Contains(out, "available=") {
		t.Errorf("InspectHealth missing fields:\n%s", out)
	}
}

func TestExplainReexports(t *testing.T) {
	// ExplainFailover
	fo := resilience.FailoverOutcome{
		Served:       resilience.Target{Provider: "backup", Model: "m"},
		Succeeded:    true,
		FailoverUsed: true,
	}
	if got := observability.ExplainFailover(fo); got == "" {
		t.Error("ExplainFailover returned empty")
	}

	// ExplainCacheHit
	entry := cache.Entry{Key: "k", Level: "l1", Value: []byte("v")}
	if got := observability.ExplainCacheHit(entry, true); got == "" {
		t.Error("ExplainCacheHit returned empty")
	}
	if got := observability.ExplainCacheHit(cache.Entry{}, false); got == "" {
		t.Error("ExplainCacheHit(miss) returned empty")
	}
}
