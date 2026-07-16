# ModelMesh — Tracing & Correlated Logging (Implementation Guide)

**Status:** Implemented (Phase 5 Part 2 — OpenTelemetry tracing + correlation)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Observability LLD](../03-components/05-observability.md) · [Metrics](./Observability-Metrics.md) · [Request Lifecycle](../02-architecture/Request-Lifecycle.md)

---

## 1. Tracing Architecture

Distributed tracing is provided by `internal/tracing`, a leaf package that hides
OpenTelemetry behind small interfaces (mirroring how `internal/metrics` hides
Prometheus):

- **`Provider`** wraps the OTel SDK tracer provider; **exporters are wired here**
  (OTLP batch for production, in-memory sync for tests). Without an exporter,
  spans still carry valid trace/span IDs for log correlation but ship nowhere.
- **`Tracer` / `Span`** hide the OTel API; subsystems depend on these interfaces.
- **`Noop()`** returns inert spans for disabled tracing (the gateway default).
- **Context propagation is automatic:** `Start` returns a context carrying the
  span, which flows through every subsystem call.

The **gateway** owns the span tree (the orchestration layer), so no lower-level
package imports OTel: it starts a root `gateway.request` span and child spans
around each phase call.

## 2. Trace Flow

Every request produces this span tree (verified end-to-end by test):

```
gateway.request  (root; attrs: request_id, provider, model, cached, cache_level)
├── gateway.route        (strategy, provider, model, score/candidates)
├── cache.lookup         (hit, level, similarity)
└── resilience.dispatch  (served, failover, attempts)
    └── provider.call    (provider, model; RecordError on failure)
```

A cache hit short-circuits before `resilience.dispatch`. Provider errors are
recorded on `provider.call` (span status = error + exception event); the root
span records the final error. This maps to Gateway → Router → Cache → Circuit
Breaker → Provider → Response.

## 3. Logging Strategy

Structured logging (slog-based `logger`) is enriched with **correlation** via
`tracing.LoggerWith(ctx, log)`, which adds `request_id`, `trace_id`, and `span_id`
from the context to every entry — so **every log line is traceable to one
request and to its trace**. The gateway emits one structured completion log per
request with `request_id`, `trace_id`, `provider`, `model`, `cached`,
`cache_level`, `score`, `latency` (and `error` on failure).

## 4. Correlation IDs

`tracing.EnsureRequestID(ctx)` generates a crypto-random `req_…` correlation ID at
request entry (or preserves an existing one), stores it in the context, sets it
as a span attribute, threads it into the routing context, and includes it in
every log. `RequestIDFromContext` / `SpanContextFromContext` retrieve the IDs.

## 5. Configuration & Wiring

```go
tp, _ := tracing.NewProvider(tracing.WithServiceName("modelmesh"), tracing.WithBatchExporter(otlpExporter))
defer tp.Shutdown(ctx)
gw := gateway.New(router, cache, cfg, gateway.WithTracer(tp.Tracer("gateway")), gateway.WithLogger(log))
```

`WithSampleRatio(r)` enables head-based sampling; default is always-sample.
`WithSyncExporter` + `tracetest.NewInMemoryExporter()` is the test path.

## 6. Exported Types Reference

| Symbol | Role |
|--------|------|
| `Provider`, `NewProvider`, `Shutdown` | OTel boundary + exporters |
| `Tracer`, `Span`, `Attribute` | Span API (hides OTel) |
| `Span*` constants | Span names |
| `SpanContextFromContext` | Trace/span IDs from ctx |
| `NewRequestID`, `EnsureRequestID`, `WithRequestID`, `RequestIDFromContext` | Correlation |
| `LoggerWith` | Correlation-enriched logger |
| `Noop` | Disabled tracing |
