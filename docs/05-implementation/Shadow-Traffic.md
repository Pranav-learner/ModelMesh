# ModelMesh — Shadow Traffic (Implementation Guide)

**Status:** Implemented (Phase 8 Part 1 — sampling, cloning, async dispatch, isolation; no comparison or reports yet)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Shadow Traffic LLD](../03-components/09-shadow-traffic.md) · [Provider Layer](./Provider-Layer.md) · [Request-Analysis](./Request-Analysis.md)

---

## 1. Purpose & Placement

Shadow Traffic duplicates a small, sampled fraction of requests to an **alternative
provider** for evaluation, **without affecting the primary response** the
application receives. Shadow requests are fire-and-forget: they run
asynchronously, their failures and panics are contained, and their results are
recorded — never returned to the caller.

```
Application → Primary Request → Provider A → response to application
                     │
                     └── (sampled) Shadow Request → Provider B → recorded only
```

`internal/shadow` is a self-contained subsystem. It depends only on the provider
types, the logger, and tracing (to capture the correlation ID). The gateway wires
it in additively via `WithShadow`; existing behavior is unchanged when it is not
wired. This part deliberately does **not** compare responses or generate reports —
that is Part 2.

## 2. Shadow Traffic Manager

`shadow.Manager` is the entry point and owns the four responsibilities:

| Responsibility | How |
|----------------|-----|
| Decide whether to shadow | delegates to the sampling `Policy` |
| Clone the request | deep-clones the mutable parts of the `ChatRequest` |
| Select a secondary provider | `Selector` over the provider source, independent of the primary |
| Dispatch asynchronously + track | background goroutine + a `tracker` of counters and recent executions |

```go
func (m *Manager) Shadow(ctx, req, primary Target) (*ShadowExecution, bool)
```

`Shadow` **never blocks** on the shadow request and **never returns an error** — it
returns `(exec, true)` when a shadow was dispatched, `(nil, false)` otherwise.
`Wait()` (for shutdown/tests) drains in-flight shadows; `Stats()` and `Recent()`
expose tracking.

## 3. Shadow Pipeline

```
Manager.Shadow(ctx, req, primary)
   ↓  tracker.evaluated
   ↓  Policy.Decide(req)              disabled? / sampled? ── not sampled → (nil, false)
   ↓  Selector.Select(primary, …)     pick a secondary provider ≠ primary ── none → skipped
   ↓  cloneRequest(req)               deep, isolated copy
   ↓  build ShadowExecution           id, ShadowRequest{clone, target}, ShadowMetadata
   ↓  go run(exec)  ───────────────▶  (async, detached context + timeout)
   return (exec, true)                     │
                                           ↓  provider.Chat on Provider B
   application already has the primary     ↓  recover panics · capture errors
   response — shadow touches nothing       ↓  record ShadowResult · tracker.completed
```

The shadow runs on a **detached `context.Background()` with its own timeout**, so
the primary's cancellation cannot reach it and its latency/timeout cannot affect
the primary.

## 4. Shadow Request Context

| Type | Role |
|------|------|
| `Target` | a `{Provider, Model}` destination |
| `ShadowRequest` | the cloned request + secondary target |
| `ShadowMetadata` | provenance: correlation ID, primary target, policy, sample rate, created-at — no response data |
| `ShadowResult` | recorded outcome: response, success, error string, latency, timing |
| `ShadowExecution` | one sampled shadow + its eventual result (`Wait()` / `Result()`) |

## 5. Sampling Strategy

The `Policy` interface decides sampling; two are implemented, one reserved:

- **Disabled** (`disabled`) — never samples. The safe default; no shadow traffic
  until explicitly enabled.
- **Fixed Percentage** (`fixed_percentage`) — samples `Percentage`% of requests
  uniformly at random (supports 1/5/10/25/50/100). The `[0,1)` random source is
  **injected**, so sampling is deterministic in tests; 0% never samples, 100%
  always does.
- **Rule Based** (`rule_based`) — reserved; `Build` returns
  `ErrPolicyNotImplemented`.

Policies resolve through a `PolicyRegistry` (name → builder), so a new strategy is
a registration, not a `switch` edit.

## 6. Secondary Provider Selection

The `Selector` chooses the secondary **independently of the primary response** —
it sees only the request and the available providers. `FirstOtherSelector` (the
default) picks the lowest-named provider that is not the primary, deterministically.
When only the primary exists, no shadow is dispatched (tracked as *skipped*).

## 7. Failure Isolation

Isolation is enforced at three levels, so a shadow can never affect the primary:

1. **Async + detached** — the shadow runs on its own goroutine and its own
   `context.Background()`+timeout; the primary has already returned.
2. **Panic recovery** — `run` recovers panics and records them as a failed
   `ShadowResult`.
3. **No shared state** — the request is deep-cloned; the shadow result is never
   written back to the primary `ChatResult`. `Shadow()` returns no error.

*Verified: a shadow provider that errors or panics leaves the primary response
untouched and the process healthy.*

## 8. Configuration

```go
sm, _ := shadow.New(
    shadow.Config{Policy: "fixed_percentage", Percentage: 5},   // shadow 5% of traffic
    providerManager,                                            // ProviderSource
    shadow.WithSampler(sampler),   // optional, for determinism
    shadow.WithSelector(sel),      // optional secondary-selection policy
    shadow.WithLogger(log),
)
gw := gateway.New(router, cache, cfg, gateway.WithShadow(sm))
```

`Config`: `Policy`, `Percentage` (0–100), `MaxTrackedExecutions`, `ShadowTimeout`.
Validated and defaulted (`DefaultConfig` is disabled).

## 9. Extension Guide

- **New sampling policy** (rule-based, per-tenant, complexity-aware) — implement
  `Policy`, `DefaultPolicyRegistry().Register(name, builder)`, drop it from
  `reservedPolicies`.
- **New secondary selection** — implement `Selector` (round-robin, weighted,
  capability-aware) and pass `WithSelector`.
- **Richer tracking / metrics** — the `tracker` counters + `Recent()` executions
  are the seam a metrics bridge or the Part 2 evaluator reads from.

## 10. Readiness for Part 2 (Comparison & Reports)

- **Both responses are already captured** — the primary is on `ChatResult`; the
  shadow's `ShadowResult` (response, latency, success) is on the recorded
  `ShadowExecution`, correlated by `Metadata.CorrelationID`.
- **Executions are retained** (`Manager.Recent()`), ready for a comparator to
  diff primary vs shadow.
- **No coupling to add** — Part 2 adds a comparison stage and a report sink that
  consume `ShadowExecution`s; the sampling, cloning, dispatch, and isolation are
  done.
