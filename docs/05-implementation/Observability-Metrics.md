# ModelMesh — Metrics, Dashboards & Diagnostics (Implementation Guide)

**Status:** Implemented (Phase 5 Part 3 — metrics wiring, Grafana dashboards, diagnostics, integration validation)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Observability LLD](../03-components/05-observability.md) · [Tracing](./Observability-Tracing.md) · [Request Lifecycle](../02-architecture/Request-Lifecycle.md)

---

## 1. Metrics Architecture

Metrics live in two layers, mirroring the tracing design (a leaf package that
hides the vendor SDK, plus a composition layer that wires it in):

- **`internal/metrics`** — a leaf package hiding Prometheus
  (`client_golang`). `Manager` owns a private `prometheus.Registry` and the
  collector factories (`Counter`/`Gauge`/`Histogram` + `*Vec`); `Metrics` is the
  typed catalog of ModelMesh's core series and exposes **recorder methods**. Every
  series is namespaced `modelmesh_…`. `Manager.Handler()` serves the standard
  `/metrics` exposition.
- **`internal/observability`** — the composition + diagnostics layer. It bridges
  subsystems into metrics (`Publish`, `BreakerListener`) and provides the
  operator-facing `Inspect*` / `Explain*` utilities. It depends on `metrics`,
  `tracing`, `resilience`, and `cache`; none of them depend on it.

**Subsystems depend on narrow recorder interfaces, never on Prometheus.** The
gateway takes a `metrics.Recorder`; the resilience health event bridge takes a
`metrics.BreakerRecorder`. The default everywhere is `metrics.NoOp{}`, so metrics
are entirely optional — a gateway built without `WithMetrics` records nothing and
allocates nothing.

```
Application → gateway (metrics.Recorder) ─┐
             resilience Monitor listener ─┼─→ internal/metrics (Prometheus) → /metrics → Prometheus → Grafana
             observability.Publish ───────┘
```

## 2. The Gateway is the Instrumentation Point

Just as the gateway owns the span tree, it owns request-path metrics — so no
lower-level package imports Prometheus. On every request the gateway records:

| Call site | Metric(s) |
|-----------|-----------|
| request completion | `gateway_requests_total{outcome}`, `gateway_request_duration_seconds` |
| routing decision | `routing_decisions_total{provider}`, `routing_decision_duration_seconds` |
| cache hit | `cache_hits_total{level}`, `cache_tokens_saved_total`, `cache_cost_saved_usd_total` |
| cache miss | `cache_misses_total` |
| provider dispatch | `provider_requests_total{provider,outcome}`, `provider_errors_total{provider}`, `provider_request_duration_seconds{provider}` |
| failover taken | `failovers_total` |

Two families are **snapshot gauges**, not per-request counters, and are set from
outside the request path (see §4): `circuit_state{provider}`,
`circuit_open_circuits`, `providers_healthy`, `providers_unhealthy`. The circuit
**transition counter** `circuit_state_changes_total{provider,to}` is driven by the
resilience event bridge.

## 3. Metrics Catalog

All names are prefixed `modelmesh_`. Latency histograms use second-scale buckets
suited to LLM timing (`0.005 … 30s`).

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `gateway_requests_total` | counter | `outcome` | End-to-end requests by success/error |
| `gateway_request_duration_seconds` | histogram | — | End-to-end latency |
| `routing_decisions_total` | counter | `provider` | Routing selections by chosen provider |
| `routing_decision_duration_seconds` | histogram | — | Time to make a routing decision |
| `cache_hits_total` | counter | `level` | Cache hits by level (l1/l2/l3) |
| `cache_misses_total` | counter | — | Cache misses (all levels) |
| `cache_tokens_saved_total` | counter | — | Tokens not spent thanks to cache hits |
| `cache_cost_saved_usd_total` | counter | — | Estimated USD saved by cache hits |
| `provider_requests_total` | counter | `provider`, `outcome` | Provider dispatches |
| `provider_errors_total` | counter | `provider` | Provider dispatch failures |
| `provider_request_duration_seconds` | histogram | `provider` | Provider call latency |
| `circuit_state_changes_total` | counter | `provider`, `to` | Breaker transitions |
| `circuit_state` | gauge | `provider` | Current breaker state (0 closed / 1 open / 2 half-open) |
| `circuit_open_circuits` | gauge | — | Count of open breakers |
| `failovers_total` | counter | — | Automatic failovers taken |
| `providers_healthy` | gauge | — | Providers currently routable |
| `providers_unhealthy` | gauge | — | Providers with an open breaker |

The circuit-state codes are exported constants (`metrics.CircuitClosedCode` = 0,
`CircuitOpenCode` = 1, `CircuitHalfOpenCode` = 2) so the resilience→metrics mapping
lives at the boundary and `metrics` stays a leaf.

## 4. Publishing Snapshot Gauges & the Breaker Bridge

Gauges reflect *instantaneous* state, so they are refreshed out-of-band rather
than per request:

```go
// After each health probe round (or on an interval):
monitor.CheckNow(ctx)
observability.Publish(met, breakers) // sets circuit_state / open_circuits / providers_{healthy,unhealthy}
```

`Publish` treats the breaker manager as the source of truth: a provider is healthy
unless its circuit is open. To count **transitions**, register the bridge once so
every state change increments `circuit_state_changes_total`:

```go
monitor.AddListener(observability.BreakerListener(met))
```

## 5. Grafana Dashboards

Seven provisioned dashboards live under `deploy/grafana/dashboards/`, one per
subsystem, each tagged `modelmesh`:

| Dashboard | Key panels |
|-----------|-----------|
| **Gateway** | Request rate, error rate, success rate, latency **P50/P95/P99**, requests by outcome |
| **Router** | Decisions by provider, **provider usage** share, decision latency P50/P95/P99 |
| **Cache** | **Cache hit rate**, tokens saved, hits by level, hits vs misses |
| **Providers** | Provider usage, error rate, latency P50 / P95 / P99 per provider |
| **Circuit Breaker** | Open circuits, **failovers**, **circuit states** (state-timeline), state changes |
| **Health** | **Healthy providers**, unhealthy providers, health over time |
| **Cost** | **Cost saved (USD)**, tokens saved, cumulative + rate |

Percentiles are computed in Grafana via
`histogram_quantile(0.95, sum(rate(<hist>_bucket[5m])) by (le))`, so the client
ships raw histogram buckets and any quantile is derivable at query time.

Dashboards are **generated** (`scripts`-style generator, committed JSON) so all
seven stay consistent, and every panel expression is **validated by test**
(`TestDashboards_ReferencedMetricsExist`) against the live registry — a dashboard
that references a metric the code doesn't emit fails CI.

## 6. Running the Stack

```bash
# 1. Fire traffic and expose /metrics:
go run ./cmd/observabilitydemo -serve      # serves :2112/metrics

# 2. Bring up Prometheus + Grafana:
docker compose -f deploy/docker-compose.yml up -d
#   Grafana    → http://localhost:3000  (admin/admin)  — dashboards auto-provisioned
#   Prometheus → http://localhost:9090
```

Prometheus scrapes `modelmesh:2112/metrics` every 10s (`deploy/prometheus/prometheus.yml`);
Grafana auto-provisions the Prometheus datasource (uid `prometheus`) and all seven
dashboards (`deploy/grafana/provisioning/`).

## 7. Diagnostics

`internal/observability` gives operators one place to inspect a running system,
independent of any external backend:

| Utility | Purpose |
|---------|---------|
| `InspectMetrics(mgr)` | Render the current metric values (what `/metrics` would serve) |
| `InspectTrace(exporter)` | Render an in-memory trace as a span tree |
| `InspectHealth(registry)` | Render live per-provider health (state, availability, latency, last error) |
| `ExplainFailover(outcome)` | Human-readable failover walk-through (re-exports `resilience`) |
| `ExplainCacheHit(entry, found)` | Explain which level served a hit and why (re-exports `cache`) |

`cmd/observabilitydemo` demonstrates all five after firing 100 requests.

## 8. Configuration & Wiring

```go
mgr := metrics.NewManager()               // or metrics.NewManager(metrics.WithNamespace("myapp"))
met := metrics.New(mgr)

gw := gateway.New(router, cache, cfg,
    gateway.WithMetrics(met),
    gateway.WithTracer(tp.Tracer("gateway")),
    gateway.WithLogger(log),
)

http.Handle("/metrics", mgr.Handler())    // expose to Prometheus
```

Omit `WithMetrics` and the gateway uses `metrics.NoOp{}` — zero overhead, no series.

## 9. Extension Guide — Adding a Metric

1. **Declare** the series in `metrics.Metrics` (a field) and register it in
   `metrics.New` via the matching `Manager` factory (namespace is applied
   automatically).
2. **Add a recorder method** on `*Metrics` and to the smallest recorder interface
   its subsystem needs (e.g. `CacheRecorder`); add the same method to `NoOp`. The
   compile-time `_ Recorder = …` assertions enforce completeness.
3. **Call it** from the owning subsystem via its injected recorder — never import
   Prometheus outside `internal/metrics`.
4. **Chart it**: add a panel to the generator, regenerate the dashboard JSON. The
   dashboard-validation test guarantees the new panel references a real metric.

Adding a whole new **subsystem recorder** follows the same pattern: define a new
narrow interface, add it to the `Recorder` union, implement on `*Metrics`/`NoOp`.
The gateway already accepts the union, so no signature changes ripple outward.
