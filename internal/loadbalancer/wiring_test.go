package loadbalancer_test

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lb "github.com/symbiotes/modelmesh/internal/loadbalancer"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
)

// Compile-time proof of the integration seams: the resilience health registry
// satisfies the balancer's HealthSource without either package importing the
// other, exactly as it satisfies routing's HealthProvider.
var _ lb.HealthSource = (*resilience.Registry)(nil)

// countingMetrics is a test observability sink.
type countingMetrics struct {
	selections atomic.Int64
	lastInst   atomic.Value // string
}

func (c *countingMetrics) RecordSelection(_, _, instanceID string) {
	c.selections.Add(1)
	c.lastInst.Store(instanceID)
}

func TestBalancer_ObservabilityAndLoggingWiring(t *testing.T) {
	metrics := &countingMetrics{}
	logs := &bytes.Buffer{}
	log := logger.NewWithWriter(logs, logger.LevelDebug)

	b := lb.New(lb.DefaultConfig(), lb.NewRoundRobin(),
		lb.WithMetrics(metrics),
		lb.WithLogger(log),
	)
	if err := b.Register(lb.Instance{ID: "a", Provider: "openai", Region: "us-east-1"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := b.Select(context.Background(), lb.Request{}); err != nil {
			t.Fatalf("select: %v", err)
		}
	}
	if metrics.selections.Load() != 3 {
		t.Errorf("metrics recorded %d selections, want 3", metrics.selections.Load())
	}
	if got, _ := metrics.lastInst.Load().(string); got != "a" {
		t.Errorf("metrics last instance = %q, want a", got)
	}
	if !strings.Contains(logs.String(), "instance selected") {
		t.Errorf("expected selection log, got: %s", logs.String())
	}
}

func TestBalancer_DiscoverAndRegistryAccessor(t *testing.T) {
	b := lb.New(lb.DefaultConfig(), lb.NewRoundRobin())
	if err := b.Discover([]lb.Instance{
		{ID: "a", Provider: "openai"},
		{ID: "b", Provider: "anthropic"},
	}); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if b.Registry().Len() != 2 {
		t.Errorf("registry Len = %d, want 2", b.Registry().Len())
	}

	// Update carrying a health state marks the instance unhealthy, gating it out.
	if err := b.Update(lb.Observation{InstanceID: "a", Latency: 10 * time.Millisecond, Health: provider.HealthStateUnhealthy}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		sel, err := b.Select(context.Background(), lb.Request{})
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		if sel.Instance.ID == "a" {
			t.Fatalf("unhealthy instance a was selected")
		}
	}
	if h := b.Statistics().HealthyCount; h != 1 {
		t.Errorf("HealthyCount = %d, want 1 (a is unhealthy)", h)
	}
}
