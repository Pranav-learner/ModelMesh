package observability_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/metrics"
)

// registeredMetricNames exercises every recorder method once, then gathers the
// registry to obtain the full set of metric family names ModelMesh exposes.
func registeredMetricNames(t *testing.T) map[string]bool {
	t.Helper()
	mgr := metrics.NewManager()
	m := metrics.New(mgr)

	// Touch every series so its family appears in Gather().
	m.GatewayRequest(true, 1)
	m.GatewayRequest(false, 1)
	m.RoutingDecision("primary", 1)
	m.CacheHit("l1")
	m.CacheMiss()
	m.AddTokensSaved(1)
	m.AddCostSaved(0.01)
	m.ProviderRequest("primary", true, 1)
	m.ProviderRequest("primary", false, 1) // populates provider_errors_total
	m.CircuitStateChange("primary", "open")
	m.SetCircuitState("primary", metrics.CircuitOpenCode)
	m.SetOpenCircuits(1)
	m.Failover()
	m.SetProvidersHealthy(1)
	m.SetProvidersUnhealthy(0)

	families, err := mgr.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range families {
		names[mf.GetName()] = true
	}
	return names
}

var metricRefRE = regexp.MustCompile(`modelmesh_[a-z0-9_]+`)

// TestDashboards_ReferencedMetricsExist parses every provisioned Grafana
// dashboard, extracts the metric names referenced in panel expressions, and
// asserts each corresponds to a real registered metric — catching dashboards
// that drift from the metrics catalog.
func TestDashboards_ReferencedMetricsExist(t *testing.T) {
	registered := registeredMetricNames(t)

	dir := filepath.Join("..", "..", "deploy", "grafana", "dashboards")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dashboards dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no dashboards found")
	}

	// Suffixes Prometheus appends to histogram families in queries.
	trimSuffix := func(name string) string {
		for _, suf := range []string{"_bucket", "_count", "_sum"} {
			if strings.HasSuffix(name, suf) {
				return strings.TrimSuffix(name, suf)
			}
		}
		return name
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		// Ensure valid JSON with panels.
		var dash struct {
			Title  string `json:"title"`
			Panels []struct {
				Targets []struct {
					Expr string `json:"expr"`
				} `json:"targets"`
			} `json:"panels"`
		}
		if err := json.Unmarshal(raw, &dash); err != nil {
			t.Fatalf("invalid dashboard JSON %s: %v", e.Name(), err)
		}
		if len(dash.Panels) == 0 {
			t.Errorf("dashboard %s has no panels", e.Name())
		}

		for _, ref := range metricRefRE.FindAllString(string(raw), -1) {
			base := trimSuffix(ref)
			if !registered[base] {
				t.Errorf("dashboard %s references unknown metric %q (base %q)", e.Name(), ref, base)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no metric references found in dashboards")
	}
	t.Logf("validated %d metric references across dashboards", checked)
}
