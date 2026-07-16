# ModelMesh — Experimentation Platform (Implementation Guide)

**Status:** Implemented (Phase 8 Part 3 — experiments, analytics, reports; finalizes ModelMesh)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Shadow Traffic](./Shadow-Traffic.md) · [Evaluation Engine](./Evaluation-Engine.md) · [Request-Analysis](./Request-Analysis.md) · [Resource-Optimization](./Resource-Optimization.md)

---

## 1. Purpose

The experimentation platform (`internal/experiment`) ties the Shadow Traffic
framework and the Evaluation Engine into **managed experiments** and turns their
results — together with routing, cache, and budget telemetry — into **analytics
reports**. It is the top of the ModelMesh stack:

```
Application → Intelligent Router → Primary + Shadow → Evaluation Engine
                                                            │
                                                     Analytics (BuildReport)
                                                            │
                                                     Experiment Reports
```

It reads only already-produced telemetry and never affects production traffic.

## 2. Experiment Manager

`Manager` owns named `Experiment`s (create / get / list / report), safe for
concurrent use. An `Experiment` bundles an `evaluation.Engine` (required) with
optional live telemetry sources and produces a `Report` on demand:

```go
em := experiment.NewManager()
exp, _ := em.Create("primary-vs-shadow", "cross-provider comparison", evalEngine,
    experiment.WithShadowManager(sm),          // sampling stats
    experiment.WithClassification(collector),  // complexity distribution
    experiment.WithCacheSavings(func() float64 { return gw.Stats().CostSavedUSD }),
    experiment.WithBudgetSavings(func() float64 { return opt.ResourceUsage().EstimatedSavingsUSD }),
    experiment.WithProviderUsage(usage.snapshot),
    experiment.WithMonthlyFactor(720),         // observed → monthly projection
)
report := exp.Report()
```

## 3. Analytics

`BuildReport(Inputs) Report` is a **pure, deterministic** assembly over a snapshot,
so analytics are testable without any live subsystem. The report carries:

| Section | Fields | Source |
|---------|--------|--------|
| Comparison | provider win rate, avg cost/latency/similarity difference, exact-match rate | Evaluation Engine |
| Usage | provider usage, classification distribution, avg complexity | tally + adaptive Collector |
| Savings | cache savings, budget savings, **estimated monthly savings** | gateway Stats + optimizer + projection |
| Verdict | deterministic recommendation | evaluation statistics |

**Estimated monthly savings** = `(cache + budget savings) × MonthlyFactor`, where
the factor projects the observed sample to monthly volume.

**Recommendation** is deterministic: promote the shadow provider when responses
match closely (similarity ≥ 0.8) **and** it is cheaper; otherwise keep the primary;
say so plainly when there is insufficient comparable data.

## 4. Diagnostics

| Utility | Shows |
|---------|-------|
| `ExplainExperiment(exp)` | Headline report: comparison, savings, recommendation |
| `InspectComparison(record)` | One evaluation in detail (quality / latency / cost / winner) |
| `EvaluationHistory(records, n)` | Recent comparisons as a table |
| `ShowRoutingDecision(decision)` | The routing decision (re-exports the adaptive diagnostic) |

## 5. Configuration

The platform is configured by composition — which telemetry sources are attached to
an experiment (all optional and nil-safe) and the `MonthlyFactor` projection. The
underlying subsystems keep their own config (see their guides). The end-to-end wiring
lives in `cmd/platformdemo`.

## 6. Demo

`cmd/platformdemo` fires 100 requests through the fully-wired platform (analysis +
adaptive routing + multi-level cache + shadow traffic + evaluation + experiment) and
prints routing decisions, shadow traffic, evaluation results, provider comparison,
cost savings, latency, similarity, and the final recommendation.

## 7. Extension Guide

- **New analytics field** — add it to `Report`/`Inputs` and populate in `BuildReport`
  (pure) plus an `Experiment` source option; no subsystem changes.
- **New report sink** — `Report` and `EvaluationRecord` are JSON-serializable;
  a persistence or dashboard exporter consumes `exp.Report()` / `exp.Records()`.
- **Rule-based experiments** — attach a shadow manager configured with a rule-based
  policy (the reserved `shadow` extension point) to target specific traffic.

## 8. Architecture Notes

Dependency direction is strictly one-way: `experiment → {evaluation, shadow,
adaptive, routing, provider, logger}`; none import `experiment`. The platform is a
read-only aggregation layer over the subsystems' snapshots — the same
composition-root discipline used throughout ModelMesh.
