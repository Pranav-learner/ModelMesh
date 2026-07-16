// Package observability is the composition and diagnostics layer for ModelMesh's
// observability platform. It connects the metrics, tracing, and resilience
// subsystems (bridges and a snapshot publisher) and provides operator-facing
// diagnostic utilities.
//
// It is a higher-level package that depends on the leaf observability packages
// (metrics, tracing) and the subsystems it observes (resilience, cache); those
// never depend on it, keeping them reusable.
//
// # Contents
//
//   - Publish: sets circuit-state and provider-health gauges from breaker
//     snapshots. Call after a health probe round or periodically.
//   - BreakerListener: a resilience health-event listener that records circuit
//     state-change counters.
//   - Diagnostics: InspectMetrics, InspectHealth, InspectTrace, and the
//     re-exported ExplainFailover / ExplainCacheHit, giving operators one place to
//     inspect the running system.
package observability
