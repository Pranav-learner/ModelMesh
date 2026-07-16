package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/metrics"
)

// scrape serves the manager's handler and returns the exposition text.
func scrape(t *testing.T, mgr *metrics.Manager) string {
	t.Helper()
	srv := httptest.NewServer(mgr.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// metricValue finds the exposition line whose prefix matches (name + labels) and
// returns its value.
func metricValue(t *testing.T, body, prefix string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return v
		}
	}
	t.Fatalf("metric %q not found in:\n%s", prefix, body)
	return 0
}

func hasMetric(body, prefix string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func TestManager_GenericPrimitives(t *testing.T) {
	mgr := metrics.NewManager()
	c := mgr.Counter("custom_total", "a counter")
	c.Inc()
	c.Add(4)
	g := mgr.Gauge("custom_gauge", "a gauge")
	g.Set(7)
	g.Dec()
	h := mgr.Histogram("custom_seconds", "a histogram", nil)
	h.Observe(0.5)

	body := scrape(t, mgr)
	if v := metricValue(t, body, "modelmesh_custom_total"); v != 5 {
		t.Errorf("custom_total = %v, want 5", v)
	}
	if v := metricValue(t, body, "modelmesh_custom_gauge"); v != 6 {
		t.Errorf("custom_gauge = %v, want 6", v)
	}
	if v := metricValue(t, body, "modelmesh_custom_seconds_count"); v != 1 {
		t.Errorf("custom_seconds_count = %v, want 1", v)
	}
}

func TestManager_CustomNamespace(t *testing.T) {
	mgr := metrics.NewManager(metrics.WithNamespace("mesh"))
	mgr.Counter("x_total", "x").Inc()
	if !hasMetric(scrape(t, mgr), "mesh_x_total") {
		t.Errorf("custom namespace not applied")
	}
}

func TestMetrics_RegistrationServesEndpoint(t *testing.T) {
	mgr := metrics.NewManager()
	_ = metrics.New(mgr)
	body := scrape(t, mgr)
	// Scalar metrics appear at zero immediately after registration.
	for _, name := range []string{
		"modelmesh_cache_misses_total",
		"modelmesh_failovers_total",
		"modelmesh_providers_healthy",
		"modelmesh_circuit_open_circuits",
	} {
		if !hasMetric(body, name) {
			t.Errorf("registered metric %q missing from endpoint", name)
		}
	}
}

func TestMetrics_Updates(t *testing.T) {
	mgr := metrics.NewManager()
	m := metrics.New(mgr)

	m.GatewayRequest(true, 10*time.Millisecond)
	m.GatewayRequest(false, 5*time.Millisecond)
	m.RoutingDecision("openai", time.Millisecond)
	m.CacheHit("l1")
	m.CacheHit("l1")
	m.CacheHit("l2")
	m.CacheMiss()
	m.AddTokensSaved(50)
	m.AddCostSaved(0.01)
	m.ProviderRequest("openai", true, 20*time.Millisecond)
	m.ProviderRequest("openai", false, 5*time.Millisecond)
	m.CircuitStateChange("openai", "open")
	m.SetCircuitState("openai", metrics.CircuitOpenCode)
	m.SetOpenCircuits(1)
	m.Failover()
	m.SetProvidersHealthy(2)
	m.SetProvidersUnhealthy(1)

	body := scrape(t, mgr)
	checks := map[string]float64{
		`modelmesh_gateway_requests_total{outcome="success"}`:                    1,
		`modelmesh_gateway_requests_total{outcome="error"}`:                      1,
		`modelmesh_gateway_request_duration_seconds_count`:                       2,
		`modelmesh_routing_decisions_total{provider="openai"}`:                   1,
		`modelmesh_cache_hits_total{level="l1"}`:                                 2,
		`modelmesh_cache_hits_total{level="l2"}`:                                 1,
		`modelmesh_cache_misses_total`:                                           1,
		`modelmesh_cache_tokens_saved_total`:                                     50,
		`modelmesh_cache_cost_saved_usd_total`:                                   0.01,
		`modelmesh_provider_requests_total{outcome="success",provider="openai"}`: 1,
		`modelmesh_provider_requests_total{outcome="error",provider="openai"}`:   1,
		`modelmesh_provider_errors_total{provider="openai"}`:                     1,
		`modelmesh_circuit_state_changes_total{provider="openai",to="open"}`:     1,
		`modelmesh_circuit_state{provider="openai"}`:                             1,
		`modelmesh_circuit_open_circuits`:                                        1,
		`modelmesh_failovers_total`:                                              1,
		`modelmesh_providers_healthy`:                                            2,
		`modelmesh_providers_unhealthy`:                                          1,
	}
	for prefix, want := range checks {
		if got := metricValue(t, body, prefix); got != want {
			t.Errorf("%s = %v, want %v", prefix, got, want)
		}
	}
}

func TestMetrics_ConcurrentUpdates(t *testing.T) {
	mgr := metrics.NewManager()
	m := metrics.New(mgr)

	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.GatewayRequest(true, time.Millisecond)
			m.CacheHit("l1")
			m.ProviderRequest("openai", true, time.Millisecond)
		}()
	}
	wg.Wait()

	body := scrape(t, mgr)
	if v := metricValue(t, body, `modelmesh_gateway_requests_total{outcome="success"}`); v != n {
		t.Errorf("gateway success = %v, want %d", v, n)
	}
	if v := metricValue(t, body, `modelmesh_cache_hits_total{level="l1"}`); v != n {
		t.Errorf("l1 hits = %v, want %d", v, n)
	}
}

func TestNoOp_DoesNotPanic(t *testing.T) {
	var r metrics.Recorder = metrics.NoOp{}
	r.GatewayRequest(true, time.Second)
	r.RoutingDecision("p", time.Second)
	r.CacheHit("l1")
	r.CacheMiss()
	r.AddTokensSaved(5)
	r.AddCostSaved(1)
	r.ProviderRequest("p", false, time.Second)
	r.CircuitStateChange("p", "open")
	r.SetCircuitState("p", 1)
	r.SetOpenCircuits(1)
	r.Failover()
	r.SetProvidersHealthy(1)
	r.SetProvidersUnhealthy(1)
}
