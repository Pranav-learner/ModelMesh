package observability

import (
	"fmt"
	"sort"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/metrics"
	"github.com/symbiotes/modelmesh/internal/resilience"
)

// InspectMetrics gathers the current metric values and renders them as sorted
// "name{labels} value" lines — a programmatic view of what /metrics would serve.
func InspectMetrics(mgr *metrics.Manager) (string, error) {
	families, err := mgr.Registry().Gather()
	if err != nil {
		return "", err
	}
	sort.Slice(families, func(i, j int) bool { return families[i].GetName() < families[j].GetName() })

	var b strings.Builder
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			labels := renderLabels(m.GetLabel())
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				fmt.Fprintf(&b, "%s%s %g\n", mf.GetName(), labels, m.GetCounter().GetValue())
			case dto.MetricType_GAUGE:
				fmt.Fprintf(&b, "%s%s %g\n", mf.GetName(), labels, m.GetGauge().GetValue())
			case dto.MetricType_HISTOGRAM:
				h := m.GetHistogram()
				fmt.Fprintf(&b, "%s_count%s %d\n", mf.GetName(), labels, h.GetSampleCount())
				fmt.Fprintf(&b, "%s_sum%s %g\n", mf.GetName(), labels, h.GetSampleSum())
			}
		}
	}
	return b.String(), nil
}

func renderLabels(labels []*dto.LabelPair) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = fmt.Sprintf("%s=%q", l.GetName(), l.GetValue())
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// InspectHealth renders the live per-provider health records from the registry.
func InspectHealth(reg *resilience.Registry) string {
	records := reg.Records()
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		r := records[name]
		fmt.Fprintf(&b, "%-16s state=%-9s available=%-5t latency=%s", name, r.State, r.Available, r.Latency)
		if r.LastError != "" {
			fmt.Fprintf(&b, " last_error=%q", r.LastError)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// InspectTrace renders the spans captured by an in-memory exporter as an
// indented tree per trace. It is a development/diagnostic utility; in production,
// traces are viewed in the tracing backend.
func InspectTrace(exp *tracetest.InMemoryExporter) string {
	spans := exp.GetSpans()

	// Index by span ID and collect children per parent.
	children := map[[8]byte][]tracetest.SpanStub{}
	roots := []tracetest.SpanStub{}
	for _, s := range spans {
		parent := s.Parent.SpanID()
		if s.Parent.IsValid() {
			children[parent] = append(children[parent], s)
		} else {
			roots = append(roots, s)
		}
	}

	var b strings.Builder
	var walk func(s tracetest.SpanStub, depth int)
	walk = func(s tracetest.SpanStub, depth int) {
		fmt.Fprintf(&b, "%s%s (%s)\n", strings.Repeat("  ", depth), s.Name, s.Status.Code)
		for _, c := range children[s.SpanContext.SpanID()] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return b.String()
}

// ExplainFailover re-exports the resilience failover diagnostic for a unified
// observability surface.
func ExplainFailover(o resilience.FailoverOutcome) string { return resilience.ExplainFailover(o) }

// ExplainCacheHit re-exports the cache hit diagnostic.
func ExplainCacheHit(e cache.Entry, found bool) string { return cache.ExplainHit(e, found) }
