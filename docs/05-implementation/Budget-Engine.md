# ModelMesh — Budget Engine (Implementation Guide)

**Status:** Implemented (Phase 6 Part 2 — per-user/per-team daily budgets, Reject + Downgrade policies)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Budget Engine LLD](../03-components/07-budget-engine.md) · [Routing Engine](./Routing-Engine.md) · [Load Balancer](./Load-Balancer.md) · [Metrics](./Observability-Metrics.md)

---

## 1. Purpose & Placement

The Budget Engine is ModelMesh's financial control plane: it answers *"can we
afford this request?"* **before** dispatch and records *"what did it cost?"*
**after**. It is completely provider-agnostic — it reasons about models, tokens,
and prices, never about a specific vendor — and enforces **per-user** and
**per-team** daily limits.

`internal/budget` is a self-contained subsystem (`Manager` façade over three
collaborators):

| Piece | Responsibility |
|-------|----------------|
| **CostModel** | Estimate cost pre-call, compute actual cost post-call, look up pricing |
| **Budget windows** | Per-scope (user/team) daily spend counters that auto-reset |
| **Policy** | Swappable enforcement strategy when a limit would be breached |

## 2. Authorize / Commit Split

Authorization and commitment are decoupled, matching the request lifecycle — a
cache hit never reaches a provider and so **never commits, never consumes budget**:

```
Authorize(req)  → estimate cost, read remaining, apply policy → Decision   (no mutation, idempotent)
   ... dispatch to provider (only on a cache miss) ...
Commit(record)  → add actual cost to the scope's daily counter             (atomic mutation)
```

`Authorize` does **not** reserve funds — it is a fast read, accepting a small,
bounded overshoot window in exchange for a lock-light hot path (the tradeoff the
LLD documents). `Commit` is the single mutation point and is safe under
concurrency (verified: 1,600 concurrent commits, exact total, `-race`).

## 3. Budget Manager

The `Manager` owns budget **lookup, tracking, updates, and validation**:

| Method | Role |
|--------|------|
| `Authorize(ctx, req) → Decision` | The decision pipeline (estimate → check → policy) |
| `Commit(ctx, record)` / `CommitUsage(...)` | Atomic actual-cost accounting |
| `Budget(scope, id) → BudgetStatus` | Lookup, auto-provisioning from scope defaults, with rollover |
| `SetBudget(Budget)` | Register/update a limit (preserves the day's usage on limit change) |
| `Usage(scope, id) → []UsageRecord` | Recent per-budget ledger (bounded) |
| `Statistics() → Statistics` | Fleet overview (per-budget status + total spend) |

State lives behind a single mutex; a distributed deployment backs the counters
with Redis (LLD §7) — the in-memory store here is operational, not an accounting
ledger.

## 4. Budget Models

One `Budget` model, distinguished by `Scope` (`user` / `team`), so a new scope is
a constant, not a new type. Constructors `UserBudget(id, limit)` /
`TeamBudget(id, limit)` make intent explicit. Live state is reported via
`BudgetStatus`:

| Field | Meaning |
|-------|---------|
| `DailyLimit` | 24h ceiling (USD) |
| `CurrentUsage` | Spend in the current window |
| `Remaining` | `DailyLimit − CurrentUsage`, floored at 0 |
| `ResetAt` | Next window boundary (UTC day-aligned) |

A budget looked up but never registered is **auto-provisioned** from
`DefaultUserDailyLimit` / `DefaultTeamDailyLimit`. Every access rolls an elapsed
window over (usage → 0, `ResetAt` advanced), so a status is always current.

## 5. Usage Accounting

Each `Commit` appends a `UsageRecord` — `Scope`, `BudgetID`, `Provider`, `Model`,
`Tokens`, `EstimatedCost`, `ActualCost`, `Timestamp` — to a bounded per-budget
ledger (`LedgerSize`, default 256, newest retained) and increments the scope's
counter. `CommitUsage` computes `ActualCost` from provider-reported
`provider.Usage` via the cost model.

## 6. Cost Estimation Strategy

`PricingModel` implements `CostModel` over a static pricing table
(`map[model]ModelPricing{InputPer1K, OutputPer1K}` + a `Default` for unlisted
models):

- **Estimate** (pre-call) = `inputTokens/1000 · InputPer1K + expectedOutputTokens/1000 · OutputPer1K`. Missing token counts fall back to configured defaults, so a caller without a tokenizer still gets a sensible number. `EstimateTokens(text)` offers a dependency-free ~4-chars/token heuristic.
- **Actual** (post-call) uses the same math over reported `PromptTokens` / `CompletionTokens`.
- **Price** is the raw lookup, exposed for observability/reporting.

The cost model is injectable (`WithCostModel`) and exposed (`Manager.CostModel()`)
so routing and observability reuse one source of truth rather than re-deriving
prices.

## 7. Policy Design

Enforcement is a pluggable `Policy` strategy (pure: reads `PolicyInput`, returns a
`Decision`, mutates nothing) resolved by name via `PolicyRegistry`:

**Reject** (`reject`, the safe default) — allow if the estimate fits remaining,
else `OutcomeReject`.

**Downgrade** (`downgrade`) — switch to the configured cheaper `DefaultModel`
when the requested model does not fit, **or proactively** when remaining budget
has fallen below `DowngradeThreshold × DailyLimit`. If even the cheaper model does
not fit → reject; if there is no cheaper option but the original fits → allow. The
`DowngradeThreshold` is what makes budget *conserving* (not just *blocking*)
possible.

Reserved extension points, nameable in config today (`Build` returns
`ErrPolicyNotImplemented`): `queue`, `notify`, `approval`.

## 8. Decision Pipeline

```
AuthorizeRequest{Scope, BudgetID, Provider, Model, InputTokens, ExpectedOutputTokens}
   ↓  CostModel.Estimate(model, tokens)
   ↓  resolve budget (auto-provision + rollover) → remaining
   ↓  Policy.Decide(estimate, status, config)
Decision{Outcome: allow | downgrade | reject, Model, EstimatedCost, Remaining, Reason}
   ↓  Allowed? → dispatch using Decision.Model
   ↓  Commit(actual cost)   ← only after a successful, billable provider call
```

## 9. Configuration

| Field | Purpose |
|-------|---------|
| `Policy` | `reject` / `downgrade` (or a reserved name) |
| `DefaultModel` | Downgrade target (required for the downgrade policy) |
| `DowngradeThreshold` | Fraction of the daily limit `[0,1)` for proactive downgrade |
| `DefaultUserDailyLimit` / `DefaultTeamDailyLimit` | Auto-provisioned limits |
| `Pricing` | Per-model pricing table + default |
| `EstimatedInputTokens` / `ExpectedOutputTokens` | Token-count defaults |
| `LedgerSize` | Bounded retained usage records per budget |

`NewManager` applies `WithDefaults`, `Validate`s (fails fast — e.g. downgrade
without a `DefaultModel`, threshold out of range, negative pricing), and resolves
the policy by name.

```go
m, err := budget.NewManager(budget.Config{
    Policy: "downgrade", DefaultModel: "gpt-4o-mini", DowngradeThreshold: 0.15,
    DefaultUserDailyLimit: 5, DefaultTeamDailyLimit: 100,
    Pricing: budget.PricingConfig{ Models: prices, Default: fallback },
})
m.SetBudget(budget.TeamBudget("team-platform", 250))

dec, _ := m.Authorize(ctx, budget.AuthorizeRequest{
    Scope: budget.ScopeUser, BudgetID: "u-42",
    Provider: "openai", Model: "gpt-4o", InputTokens: 1200, ExpectedOutputTokens: 400,
})
if !dec.Allowed() { return errBudgetExceeded }
resp, err := dispatch(dec.Model, ...)          // use possibly-downgraded model
m.CommitUsage(ctx, budget.ScopeUser, "u-42", "openai", dec.Model, resp.Usage, dec.EstimatedCost)
```

## 10. Extension Guide

**New policy** (e.g. Queue): implement `Policy` (`Name` + `Decide`), then
`DefaultPolicyRegistry().Register("queue", builder)` and drop it from
`reservedPolicies`. It becomes selectable by config name with no change to the
Manager.

**New scope** (e.g. per-org): add a `Scope` constant + `valid()` arm and an
`OrgBudget` constructor; the `key(scope,id)` accounting is already scope-generic.

**Dynamic pricing / distributed counters**: swap the `CostModel` via
`WithCostModel`, or back the counters with Redis behind the same `Manager` surface
(LLD §7/§12) — `Authorize`/`Commit` semantics are unchanged.

**Integration**: `Manager.CostModel()` bridges into the observability cost metrics
(`cache_cost_saved_usd_total` and friends) so budget and dashboards share one
pricing source; `Decision.Outcome == downgrade` composes with the Routing Engine
(routing selects the substitute model's provider) exactly as the LLD's `reroute`
verdict intends.
