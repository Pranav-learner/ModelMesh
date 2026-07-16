package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Span names for the request trace, mirroring the Request Lifecycle stages.
const (
	SpanRequest      = "gateway.request"
	SpanRoute        = "gateway.route"
	SpanCacheLookup  = "cache.lookup"
	SpanDispatch     = "resilience.dispatch"
	SpanProviderCall = "provider.call"
)

// Attribute is a span key/value pair. It hides the OTel attribute type.
type Attribute struct {
	Key   string
	Value any
}

// Attribute constructors.
func String(k, v string) Attribute  { return Attribute{Key: k, Value: v} }
func Int(k string, v int) Attribute { return Attribute{Key: k, Value: v} }
func Float(k string, v float64) Attribute {
	return Attribute{Key: k, Value: v}
}
func Bool(k string, v bool) Attribute { return Attribute{Key: k, Value: v} }

// Span abstracts an OTel span. It is safe to call on a no-op span.
type Span interface {
	// End completes the span.
	End()
	// SetAttributes adds attributes to the span.
	SetAttributes(attrs ...Attribute)
	// RecordError records an error and marks the span's status as failed.
	RecordError(err error)
	// SetStatus marks the span ok or failed with a description.
	SetStatus(ok bool, description string)
	// TraceID / SpanID return the span's IDs ("" for a no-op span).
	TraceID() string
	SpanID() string
}

// Tracer abstracts an OTel tracer.
type Tracer interface {
	// Start begins a span as a child of any span in ctx, returning a context that
	// carries the new span (for propagation) and the span itself.
	Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span)
}

// --- OTel-backed implementation ---

type otelTracer struct{ tracer oteltrace.Tracer }

func (t otelTracer) Start(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span) {
	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithAttributes(toKeyValues(attrs)...))
	return ctx, otelSpan{span: span}
}

type otelSpan struct{ span oteltrace.Span }

func (s otelSpan) End()                             { s.span.End() }
func (s otelSpan) SetAttributes(attrs ...Attribute) { s.span.SetAttributes(toKeyValues(attrs)...) }
func (s otelSpan) TraceID() string                  { return s.span.SpanContext().TraceID().String() }
func (s otelSpan) SpanID() string                   { return s.span.SpanContext().SpanID().String() }

func (s otelSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

func (s otelSpan) SetStatus(ok bool, description string) {
	if ok {
		s.span.SetStatus(codes.Ok, description)
		return
	}
	s.span.SetStatus(codes.Error, description)
}

// toKeyValues converts abstraction attributes to OTel key/values.
func toKeyValues(attrs []Attribute) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		switch v := a.Value.(type) {
		case string:
			out = append(out, attribute.String(a.Key, v))
		case int:
			out = append(out, attribute.Int(a.Key, v))
		case int64:
			out = append(out, attribute.Int64(a.Key, v))
		case float64:
			out = append(out, attribute.Float64(a.Key, v))
		case bool:
			out = append(out, attribute.Bool(a.Key, v))
		default:
			out = append(out, attribute.String(a.Key, fmt.Sprint(v)))
		}
	}
	return out
}

// SpanContextFromContext returns the trace and span IDs of the active span in
// ctx, and whether a valid span is present.
func SpanContextFromContext(ctx context.Context) (traceID, spanID string, ok bool) {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", "", false
	}
	return sc.TraceID().String(), sc.SpanID().String(), true
}

// --- no-op implementation ---

// Noop returns a tracer that produces inert spans, for disabled tracing.
func Noop() Tracer { return noopTracer{} }

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ ...Attribute) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) End()                       {}
func (noopSpan) SetAttributes(...Attribute) {}
func (noopSpan) RecordError(error)          {}
func (noopSpan) SetStatus(bool, string)     {}
func (noopSpan) TraceID() string            { return "" }
func (noopSpan) SpanID() string             { return "" }
