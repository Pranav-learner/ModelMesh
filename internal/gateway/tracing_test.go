package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

func TestTracing_RequestProducesSpanTreeAndCorrelatedLogs(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp, err := tracing.NewProvider(tracing.WithSyncExporter(exp))
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, logger.LevelInfo)

	primary := newFlaky("primary", true)
	reg := provider.NewRegistry()
	_ = reg.Register(primary)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("primary"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatalf("routing.Build() = %v", err)
	}
	breakers := resilience.NewManager(resilience.DefaultConfig())
	failover := resilience.NewFailover(breakers)

	l1 := cache.NewMemoryCache(cache.DefaultConfig().Memory)
	defer func() { _ = l1.Close() }()
	cm := cache.NewManager([]cache.Cache{l1})

	gw := gateway.New(router, cm, cache.DefaultConfig(),
		gateway.WithFailover(failover, pm),
		gateway.WithTracer(tp.Tracer("gateway")),
		gateway.WithLogger(log),
	)

	res, err := gw.Chat(context.Background(), chatReq("trace me"))
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Response.Provider != "primary" {
		t.Fatalf("served by %q, want primary", res.Response.Provider)
	}

	// --- span tree ---
	spans := exp.GetSpans()
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	for _, name := range []string{tracing.SpanRequest, tracing.SpanRoute, tracing.SpanCacheLookup, tracing.SpanDispatch, tracing.SpanProviderCall} {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing span %q (got %v)", name, spanNames(spans))
		}
	}

	root := byName[tracing.SpanRequest]
	// All spans share the root's trace.
	for _, s := range spans {
		if s.SpanContext.TraceID() != root.SpanContext.TraceID() {
			t.Errorf("span %q is in a different trace", s.Name)
		}
	}
	// Parentage: route/cache/dispatch under request; provider.call under dispatch.
	assertParent(t, byName, tracing.SpanRoute, root.SpanContext.SpanID().String())
	assertParent(t, byName, tracing.SpanCacheLookup, root.SpanContext.SpanID().String())
	assertParent(t, byName, tracing.SpanDispatch, root.SpanContext.SpanID().String())
	assertParent(t, byName, tracing.SpanProviderCall, byName[tracing.SpanDispatch].SpanContext.SpanID().String())

	// The request span carries the correlation ID.
	if !spanHasStringAttr(root, "request_id") {
		t.Errorf("request span missing request_id attribute")
	}

	// --- correlated structured log ---
	entry := findLog(t, buf.String(), "request completed")
	if entry["request_id"] == nil || entry["trace_id"] == nil {
		t.Errorf("completion log missing correlation IDs: %v", entry)
	}
	if entry["trace_id"] != root.SpanContext.TraceID().String() {
		t.Errorf("log trace_id %v does not match trace %s", entry["trace_id"], root.SpanContext.TraceID())
	}
	if entry["provider"] != "primary" {
		t.Errorf("completion log provider = %v, want primary", entry["provider"])
	}
}

// --- helpers ---

func spanNames(spans tracetest.SpanStubs) []string {
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Name
	}
	return out
}

func assertParent(t *testing.T, byName map[string]tracetest.SpanStub, child, wantParentSpanID string) {
	t.Helper()
	s, ok := byName[child]
	if !ok {
		return
	}
	if s.Parent.SpanID().String() != wantParentSpanID {
		t.Errorf("span %q parent = %s, want %s", child, s.Parent.SpanID(), wantParentSpanID)
	}
}

func spanHasStringAttr(s tracetest.SpanStub, key string) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key && a.Value.AsString() != "" {
			return true
		}
	}
	return false
}

func findLog(t *testing.T, output, msg string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, msg) {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line not JSON: %v\n%s", err, line)
		}
		return entry
	}
	t.Fatalf("log %q not found in:\n%s", msg, output)
	return nil
}
