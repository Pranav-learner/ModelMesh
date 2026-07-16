// Package tracing is ModelMesh's distributed-tracing and correlation foundation.
// It hides OpenTelemetry behind small internal interfaces so the rest of the
// application never imports the OTel API directly, and provides request
// correlation IDs plus a helper that enriches structured logs with trace context.
//
// # Scope (Phase 5 Part 2)
//
// This package provides tracing and correlation only: a Provider that builds the
// OTel SDK tracer provider and serves tracers, Tracer/Span abstractions, span
// name constants, context propagation helpers, correlation (request) IDs, and a
// logger enrichment that adds request_id/trace_id/span_id to every entry. It does
// NOT implement Grafana dashboards or alerting.
//
// # Design
//
//   - Provider wraps *sdktrace.TracerProvider; exporters are wired here (the OTel
//     boundary), analogous to the metrics Manager wrapping Prometheus.
//   - Tracer/Span hide the OTel API; subsystems depend on these interfaces.
//   - Context propagation is automatic: Start returns a context carrying the span,
//     which flows through every subsystem call.
//   - Noop returns a tracer that creates inert spans, for disabled tracing.
//
// The package imports only the OTel client and the logger (a leaf), so it never
// couples tracing to a subsystem.
package tracing
