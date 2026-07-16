package tracing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

// newTestProvider builds a provider with a synchronous in-memory exporter.
func newTestProvider(t *testing.T) (*tracing.Provider, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	p, err := tracing.NewProvider(tracing.WithServiceName("test"), tracing.WithSyncExporter(exp))
	if err != nil {
		t.Fatalf("NewProvider() = %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, exp
}

func TestTracing_TraceGeneration(t *testing.T) {
	p, exp := newTestProvider(t)
	tracer := p.Tracer("gw")

	_, span := tracer.Start(context.Background(), tracing.SpanRequest, tracing.String("model", "gpt-4o"))
	span.SetAttributes(tracing.Bool("cached", true))
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans, want 1", len(spans))
	}
	s := spans[0]
	if s.Name != tracing.SpanRequest {
		t.Errorf("span name = %q, want %q", s.Name, tracing.SpanRequest)
	}
	if !hasAttr(s, "model", "gpt-4o") || !hasBoolAttr(s, "cached", true) {
		t.Errorf("span attributes = %v", s.Attributes)
	}
}

func TestTracing_ContextPropagation(t *testing.T) {
	p, exp := newTestProvider(t)
	tracer := p.Tracer("gw")

	ctx, root := tracer.Start(context.Background(), tracing.SpanRequest)
	// A child started from the root's context must share the trace and parent to it.
	_, child := tracer.Start(ctx, tracing.SpanRoute)
	child.End()
	root.End()

	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("exported %d spans, want 2", len(spans))
	}
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	rootStub, childStub := byName[tracing.SpanRequest], byName[tracing.SpanRoute]
	if rootStub.SpanContext.TraceID() != childStub.SpanContext.TraceID() {
		t.Errorf("child not in the same trace as root")
	}
	if childStub.Parent.SpanID() != rootStub.SpanContext.SpanID() {
		t.Errorf("child parent = %s, want root %s", childStub.Parent.SpanID(), rootStub.SpanContext.SpanID())
	}
}

func TestTracing_SpanContextFromContext(t *testing.T) {
	p, _ := newTestProvider(t)
	tracer := p.Tracer("gw")

	ctx, span := tracer.Start(context.Background(), tracing.SpanRequest)
	defer span.End()

	traceID, spanID, ok := tracing.SpanContextFromContext(ctx)
	if !ok {
		t.Fatalf("no span context in ctx")
	}
	if traceID != span.TraceID() || spanID != span.SpanID() {
		t.Errorf("context IDs (%s/%s) != span IDs (%s/%s)", traceID, spanID, span.TraceID(), span.SpanID())
	}

	// A bare context has no span.
	if _, _, ok := tracing.SpanContextFromContext(context.Background()); ok {
		t.Errorf("bare context reported a valid span")
	}
}

func TestTracing_RecordError(t *testing.T) {
	p, exp := newTestProvider(t)
	tracer := p.Tracer("gw")

	_, span := tracer.Start(context.Background(), tracing.SpanProviderCall)
	span.RecordError(errors.New("upstream down"))
	span.End()

	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want error", spans[0].Status)
	}
	if len(spans[0].Events) == 0 {
		t.Errorf("RecordError did not add an exception event")
	}
}

func TestTracing_Noop(t *testing.T) {
	tracer := tracing.Noop()
	ctx, span := tracer.Start(context.Background(), tracing.SpanRequest)
	span.SetAttributes(tracing.String("k", "v"))
	span.RecordError(errors.New("x"))
	span.End()
	if span.TraceID() != "" || span.SpanID() != "" {
		t.Errorf("no-op span has non-empty IDs")
	}
	if _, _, ok := tracing.SpanContextFromContext(ctx); ok {
		t.Errorf("no-op tracer produced a valid span context")
	}
}

func TestCorrelation_RequestID(t *testing.T) {
	a, b := tracing.NewRequestID(), tracing.NewRequestID()
	if a == b {
		t.Errorf("NewRequestID not unique: %s", a)
	}
	if !strings.HasPrefix(a, "req_") {
		t.Errorf("request ID missing prefix: %s", a)
	}

	ctx, id := tracing.EnsureRequestID(context.Background())
	if got, ok := tracing.RequestIDFromContext(ctx); !ok || got != id {
		t.Errorf("EnsureRequestID/RequestIDFromContext mismatch: %s vs %s", got, id)
	}
	// EnsureRequestID is idempotent: an existing ID is preserved.
	ctx2, id2 := tracing.EnsureRequestID(ctx)
	if id2 != id || ctx2 != ctx {
		t.Errorf("EnsureRequestID replaced an existing ID")
	}
}

func TestCorrelation_LoggerWith(t *testing.T) {
	p, _ := newTestProvider(t)
	tracer := p.Tracer("gw")

	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, logger.LevelInfo)

	ctx := tracing.WithRequestID(context.Background(), "req_abc")
	ctx, span := tracer.Start(ctx, tracing.SpanRequest)
	defer span.End()

	tracing.LoggerWith(ctx, log).Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log not JSON: %v\n%s", err, buf.String())
	}
	if entry["request_id"] != "req_abc" {
		t.Errorf("missing request_id: %v", entry)
	}
	if entry["trace_id"] != span.TraceID() || entry["span_id"] != span.SpanID() {
		t.Errorf("log trace/span IDs do not match the active span: %v", entry)
	}
}

func TestCorrelation_LoggerWith_NoContext(t *testing.T) {
	log := logger.Nop()
	if got := tracing.LoggerWith(context.Background(), log); got != log {
		t.Errorf("LoggerWith with no correlation should return the original logger")
	}
}

// --- helpers ---

func hasAttr(s tracetest.SpanStub, key, value string) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key && a.Value.AsString() == value {
			return true
		}
	}
	return false
}

func hasBoolAttr(s tracetest.SpanStub, key string, value bool) bool {
	for _, a := range s.Attributes {
		if string(a.Key) == key && a.Value.AsBool() == value {
			return true
		}
	}
	return false
}
