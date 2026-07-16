# ModelMesh — Load Balancer (Implementation Guide)

**Status:** Implemented (Phase 6 Part 1 — Round Robin + Least Latency over a live instance pool)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Load Balancer LLD](../03-components/06-load-balancer.md) · [Routing Engine](./Routing-Engine.md) · [Resilience](./Resilience.md) · [Metrics](./Observability-Metrics.md)

---

## 1. Where the Load Balancer Sits

The Routing Engine decides *which logical provider/model* serves a request (the
semantic choice). The Load Balancer decides *which concrete instance* of that
provider receives it — e.g. OpenAI `us-east-1` vs `eu-west-1` vs `us-west-2`. It
never reconsiders provider or model and never owns health; it **consumes** health
from the Resilience layer and distributes load across equivalent instances.

```
Routing Engine → provider/model → Load Balancer → instance → Provider dispatch
```

`internal/loadbalancer` is a self-contained subsystem built from three orthogonal
pieces so new algorithms plug in without touching existing code:

| Piece | Type | Responsibility |
|-------|------|----------------|
| **Instance / InstanceRegistry** | state | The routable instances + live runtime state (health, rolling latency, request count, last used) |
| **Strategy** | algorithm | The pluggable selection policy over a candidate snapshot |
| **Balancer** (`LoadBalancer`) | façade | Wires registry + strategy, closes the feedback loop |

## 2. The LoadBalancer Interface

```go
type LoadBalancer interface {
    Select(ctx context.Context, req Request) (Selection, error)
    Register(inst Instance) error
    Remove(id string) error
    Update(obs Observation) error
    Statistics() Statistics
}
```

`*Balancer` implements it (compile-time asserted). Operator lifecycle methods
(`Enable` / `Disable` / `Discover` / `SetHealth`) live on the `InstanceRegistry`,
reachable via `Balancer.Registry()` and mirrored as pass-throughs on the balancer.

## 3. Instance Model

An `Instance` is an immutable descriptor; its mutable runtime state lives in the
registry and is reported via `InstanceStats`:

| Field | Source | Notes |
|-------|--------|-------|
| `ID` | descriptor | Unique within the balancer (required) |
| `Provider` | descriptor | Logical provider name (required); drives health gating + request filter |
| `Region` | descriptor | Deployment region label |
| `Weight` | descriptor | Reserved for weighted strategies |
| `Client` | descriptor | Optional `provider.LLMProvider` so the selected instance is directly dispatchable |
| `Health` | runtime | `provider.HealthState`; set via `SetHealth` or an `Observation` |
| `AverageLatency` | runtime | Rolling mean over recent requests ("current latency") |
| `RequestCount` | runtime | Times selected |
| `LastUsed` | runtime | Last selection timestamp (injectable clock) |

## 4. Instance Registry

`InstanceRegistry` is the concurrency-safe source of truth (`sync.RWMutex`,
insertion-ordered for deterministic listing):

- **Register / Deregister** — add / remove, validating required fields (`ErrInvalidInstance`, `ErrInstanceExists`, `ErrInstanceNotFound`).
- **Enable / Disable** — toggle eligibility **without discarding stats**, so a disabled instance resumes with its history intact.
- **Discover** — reconcile against a desired set: add new, drop absent, **retain existing instances' runtime state** (refreshing their descriptor). This is the service-discovery refresh seam.
- **SetHealth / Update** — update health / record a latency sample.

## 5. Latency Tracking (§ requirement: no Prometheus)

Each instance owns a `rollingLatency` — a fixed-size ring buffer with a running
sum, so `record` and `average` are both **O(1)**. The window defaults to the last
20 requests (`Config.LatencyWindow`). It has **no dependency on Prometheus or any
metrics backend**: latency-aware selection works with zero observability wired.
Prometheus is fed separately and optionally via the `Metrics` seam (§8).

## 6. Selection Pipeline

```
Request
  ↓  Balancer.Select
enumerate registry snapshots
  ↓  filter: provider match → enabled → instance health → provider health (HealthSource)
  ↓  sort by ID (deterministic order)
Strategy.Pick(candidates)          ← pure; strategy holds only its own cursor
  ↓
markSelected (RequestCount++, LastUsed=now)   ← under registry lock
  ↓
Selection{Instance, Strategy, Stats}
  ↓  caller dispatches to Instance.Client / resolves by name
Update(Observation{Latency, Success, Health})  ← closes the loop
```

The strategy is handed **snapshots**, never the live registry, and runs outside
the registry lock; only the counter bump re-takes the lock. Empty eligibility
yields `ErrNoInstances`, letting the caller fall back to the next routing
candidate.

## 7. Load Balancing Algorithms

**Round Robin** (`round_robin`) — an atomic cursor cycles the ID-sorted eligible
candidates, advancing once per pick. Distribution stays even across the currently
eligible set even as instances are enabled/disabled/removed.

**Least Latency** (`least_latency`) — picks the lowest rolling-average latency.
Unmeasured instances are **explored first** (so every instance is sampled before
exploitation and a fresh instance is never starved), then ties break by fewer
requests, then ID — fully deterministic.

Reserved extension points, nameable in config today (`Build` returns
`ErrStrategyNotImplemented`): `weighted_round_robin`, `least_connections`,
`random`, `consistent_hashing` (the `Request.Key` field already carries the
partition key the last one needs).

## 8. Integration Seams

- **Provider Layer** — `Instance.Client` optionally carries the concrete
  `provider.LLMProvider`, so the selection is directly dispatchable.
- **Resilience** — `WithHealthSource(h HealthSource)` gates unhealthy providers
  out of selection. `HealthSource.Health(provider) (provider.HealthStatus, bool)`
  is structurally satisfied by `*resilience.Registry` (compile-time asserted in
  tests) — no import between the packages, same trick routing uses for its
  `HealthProvider`.
- **Observability** — `WithMetrics(Metrics)` records each selection
  (strategy/provider/instance). Default `NopMetrics`; the composition layer can
  bridge it into the metrics catalog. `Statistics()` exposes the full read model
  for logs/diagnostics.

## 9. Statistics

`Statistics()` returns strategy name, pool composition (`TotalInstances` /
`EnabledCount` / `HealthyCount`), running `TotalSelections`, and per-instance
`InstanceStats`. `HealthyCount` and each `Healthy` flag reflect **full
routability** (enabled + instance health + provider health via any wired
HealthSource) — i.e. what `Select` would actually consider eligible.

## 10. Configuration & Wiring

```go
b, err := loadbalancer.Build(loadbalancer.Config{Strategy: "least_latency"},
    loadbalancer.WithHealthSource(healthRegistry), // *resilience.Registry
    loadbalancer.WithMetrics(lbMetrics),
    loadbalancer.WithLogger(log),
)
b.Discover([]loadbalancer.Instance{
    {ID: "openai-us-east-1", Provider: "openai", Region: "us-east-1", Client: oaEast},
    {ID: "openai-eu-west-1", Provider: "openai", Region: "eu-west-1", Client: oaWest},
})

sel, err := b.Select(ctx, loadbalancer.Request{Provider: "openai"})
resp, err := sel.Instance.Client.Chat(ctx, req)
b.Update(loadbalancer.Observation{InstanceID: sel.Instance.ID, Latency: elapsed, Success: err == nil})
```

`New(cfg, strategy, opts...)` injects a strategy directly; `Build(cfg, opts...)`
resolves it by name via `DefaultRegistry`.

## 11. Extension Guide — Adding a Strategy

1. Implement `Strategy` (`Name` + `Pick`) in a new file — pure over the candidate
   snapshot, holding only its own state, safe for concurrent `Pick`.
2. Register a builder: `DefaultRegistry().Register("my_strategy", func(Config) (Strategy, error) {...})`,
   or add it to `DefaultRegistry` and drop the name from `reservedStrategies`.
3. It is now selectable by config name via `Build`, with no change to the
   balancer, registry, or callers.

New per-instance signals (e.g. in-flight count for Least Connections) are added to
`managedInstance` + `InstanceStats` and populated in `markSelected` / `Update`;
strategies then read them off the `Candidate.Stats` snapshot.
