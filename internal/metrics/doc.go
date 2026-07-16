// Package metrics is ModelMesh's centralized metrics foundation. It hides
// Prometheus behind small internal interfaces so the rest of the application
// never imports the Prometheus client directly.
//
// # Scope (Phase 5 Part 1)
//
// This package provides the metrics layer only: a Manager that registers metrics
// and serves the /metrics endpoint, abstraction primitives (Counter/Gauge/
// Histogram and their labeled variants), a typed catalog of the core ModelMesh
// metrics with recorder methods, and a no-op recorder. It does NOT implement
// distributed tracing, Grafana dashboards, or alerting. Wiring each subsystem to
// publish is Phase 5 Part 2.
//
// # Design
//
//   - Manager wraps a prometheus.Registry and exposes factory methods returning
//     the abstraction primitives, plus Handler() for the scrape endpoint.
//   - Metrics is a typed facade over the core metric set (gateway, router, cache,
//     provider, circuit breaker, health). Subsystems depend on the small recorder
//     interfaces (GatewayRecorder, RouterRecorder, ...), not on Prometheus.
//   - NoOp implements every recorder interface for tests and disabled metrics.
//
// The package is a leaf: it imports only the Prometheus client and net/http, so
// it never couples the metrics layer to any subsystem.
package metrics
