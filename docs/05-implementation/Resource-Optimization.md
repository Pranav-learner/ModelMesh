# ModelMesh — Resource Optimization (Implementation Guide)

**Status:** Implemented (Phase 6 Part 3 — Budget + Router + Load Balancer integrated into the request pipeline)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Load Balancer](./Load-Balancer.md) · [Budget Engine](./Budget-Engine.md) · [Routing Engine](./Routing-Engine.md) · [Observability-Metrics](./Observability-Metrics.md)

---

## 1. What This Layer Does

Phase 6 built two subsystems in isolation — the **Load Balancer** (Part 1,
distributes traffic across provider instances) and the **Budget Engine** (Part 2,
controls spend). Part 3 composes them, with the Routing Engine, into a single
pre-dispatch **optimization layer** so ModelMesh optimizes *both* infrastructure
cost and traffic distribution on every request.

The coordinator is `internal/optimization.Optimizer`. It depends on the three
subsystems through **narrow interfaces** (`Router`, `Budget`, `LoadBalancer`) —
none of them depend on each other or on the optimizer, so each remains
independently testable and reusable.

## 2. The Optimization Pipeline

```
Application (gateway)
   ↓  OptimizeRequest{ chat, scope, budgetID }
   ↓  Routing Engine        choose provider + model            (router.Route)
   ↓  Budget Engine         authorize estimated cost           (budget.Authorize)
   │     ├─ allow     → proceed
   │     ├─ reject    → Plan.Rejected → gateway returns ErrBudgetExceeded
   │     └─ downgrade → re-run routing with the cheaper model  (router.Route again)
   ↓  Load Balancer         choose a concrete instance         (lb.Select)
   Plan{ provider, model, instance, budget decision, savings }
   ↓  gateway dispatches to instance.Client (or provider by name)
   ↓  Commit                 actual cost → budget, latency → load balancer
```

`Optimize` never dispatches — it returns a `Plan`. The gateway dispatches, then
calls `Commit`, which is the single accounting mutation (only on a successful,
billable call, so **cache hits consume no budget**). Budget and Load Balancer are
both optional: a nil collaborator skips that stage, so the same coordinator serves
cost-only, balance-only, or fully-optimized deployments.

## 3. Gateway Integration

The wiring is **additive** — existing dispatch paths are untouched. `gateway.New`
gains `WithOptimizer(o)`; when set, `Chat` routes through a new `chatOptimized`
path that runs the pipeline, serves from cache under the (possibly downgraded)
model's key, dispatches to the chosen instance, and commits. Budget identity
travels on request metadata:

| Metadata key | Meaning |
|--------------|---------|
| `budget_scope` | `user` (default) or `team` |
| `budget_id` | the user/team identifier (absent → budget stage skipped) |

Dispatch prefers the selected instance's `Client`; with no instance it falls back
to the named provider via the resolver (`WithProviderResolver` / `WithFailover`).

## 4. Budget + Router Cooperation (Downgrade Strategy)

When the routed model does not fit the remaining budget — or remaining has fallen
below the configured `DowngradeThreshold` — the Downgrade policy substitutes the
cheaper `DefaultModel`. The optimizer then **re-runs routing constrained to that
model** (`RoutingContext.Model`), so the *best provider for the cheaper model* is
chosen (which may differ from the original provider). If no provider advertises
the downgraded model, it falls back to the original provider serving the cheaper
model. Estimated savings = (original-model estimate − downgraded estimate) and are
tracked and reported. A `reject`-policy budget instead surfaces
`optimization.ErrBudgetExceeded` and never dispatches.

## 5. Load Balancer + Router Cooperation

Clean separation of concerns:
- **Router** decides *which provider + model* (semantic choice, health/score-aware).
- **Load Balancer** decides *which concrete instance* of that provider (distribution, latency-aware).

The optimizer calls `lb.Select(Request{Provider: chosenProvider})`; a provider
with no registered instances simply yields no instance and dispatch falls back to
the provider name — so the balancer is strictly additive.

## 6. Resource Metrics

Emitted through the `ResourceMetrics` seam (no-op default; no Prometheus
dependency, bridgeable by the observability layer):

| Metric | Source |
|--------|--------|
| Budget Usage | `BudgetUsage(scope, id, used, remaining)` on authorize/commit |
| Downgrades | `Downgrade(from, to)` when a model is substituted |
| Rejected Requests | `Reject(scope, id)` on a budget rejection |
| Load Distribution | `LoadSelection(provider, instance)` per selection |
| Instance Utilization | `loadbalancer.Statistics()` (request count, latency) |
| Estimated Savings | `EstimatedSavings(usd)` per downgrade |

`Optimizer.ResourceUsage()` returns a combined read model (optimizer counters +
budget statuses + instance stats) backing the "display resource usage" diagnostic.

## 7. Diagnostics

| Utility | Purpose |
|---------|---------|
| `ExplainBudgetDecision(d)` | Render a budget verdict (outcome, estimate, remaining, reason) |
| `ExplainDowngrade(plan)` | Explain a model downgrade and its savings |
| `ExplainLoadBalancing(sel)` | Explain an instance selection (provider, instance, region, latency) |
| `ExplainPlan(plan)` | The full optimization decision for one request |
| `Optimizer.ResourceUsage()` | Combined budget + load-distribution + savings snapshot |

## 8. Architecture

```
                internal/optimization  (coordinator)
                 │        │        │
   Router ───────┘        │        └─────── LoadBalancer
   (routing.Manager)      │                 (loadbalancer.Balancer)
                    Budget (budget.Manager)
```

Dependency direction is one-way: `optimization → {routing, budget, loadbalancer,
provider}`, and `gateway → optimization`. No subsystem imports the optimizer or
each other; the coordinator is the only place that knows about all three. This is
the same composition-root discipline used elsewhere in ModelMesh.

## 9. Configuration

The optimizer itself is configured by composition (which collaborators are wired);
each subsystem keeps its own config:

```go
router, _ := routing.Build(pm, routing.DefaultConfig())
bm, _     := budget.NewManager(budget.Config{Policy: "downgrade", DefaultModel: "gpt-4o-mini", Pricing: prices})
lb        := loadbalancer.New(loadbalancer.Config{Strategy: "least_latency"}, loadbalancer.NewLeastLatency())

opt := optimization.New(router,
    optimization.WithBudget(bm),
    optimization.WithLoadBalancer(lb),
    optimization.WithMetrics(resourceMetrics),
)
gw := gateway.New(router, cache, cfg, gateway.WithOptimizer(opt))
```

See [Budget-Engine.md](./Budget-Engine.md) §9 and [Load-Balancer.md](./Load-Balancer.md) §10 for each subsystem's configuration.

## 10. Extension Guide

- **New selection strategy / budget policy** — add it to the respective
  subsystem's registry (see their extension guides); the optimizer picks it up
  with no change, since it depends only on the narrow interfaces.
- **New pipeline stage** (e.g. a Phase 7 complexity classifier before routing) —
  the optimizer's stages are sequential and each mutates the `Plan`; a new stage
  is a method that runs before/after the existing ones and enriches the plan.
  `RoutingContext.Attributes` already carries a forward-compatible slot for a
  complexity signal, so no contract changes are needed.
- **Bridging metrics** — implement `ResourceMetrics` (and `loadbalancer.Metrics`)
  against `internal/metrics` to surface optimization on the Grafana dashboards.

## 11. Demo

`cmd/optimizationdemo` runs the full flow offline: a user streams requests on
gpt-4; as the daily budget drains, the gateway downgrades to gpt-4o-mini, the
router re-routes to the provider serving it, and the load balancer spreads the
calls — printing chosen model / provider / instance, budget remaining, and cost
saved per request, then a resource-usage summary.
