# ModelMesh — Evaluation Engine (Implementation Guide)

**Status:** Implemented (Phase 8 Part 2 — deterministic comparison of primary vs shadow: quality, latency, cost + statistics)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Shadow Traffic](./Shadow-Traffic.md) · [Budget Engine](./Budget-Engine.md) · [Provider Layer](./Provider-Layer.md)

---

## 1. Purpose & Placement

The Evaluation Engine compares each **primary** response against its **shadow**
counterpart and produces deterministic quality, latency, and cost metrics for
offline analysis. It consumes the pairs the Shadow Traffic framework emits and
stores an `EvaluationRecord` per pair; aggregate `Statistics` are computed over the
stored records. It is deliberately **not** an LLM-as-a-Judge system — every metric
is a cheap, deterministic function of the two responses.

`internal/evaluation` runs entirely off the production path: it is invoked on the
shadow goroutine (already off the primary path) and only reads responses that have
already been produced.

## 2. Evaluation Architecture

```
shadow.Comparison ──▶ Engine.Evaluate ──▶ Engine.Compare ──▶ ComparisonResult
   (primary + shadow)        │                                      │
                             ▼                                      ▼
                     EvaluationRecord ──▶ store (bounded)  ──▶ Engine.Statistics()
```

The connection to Part 1 is a **dependency-inverted seam**: `shadow` defines the
`Evaluator` interface and the `Comparison` input; `evaluation.Engine` implements
`shadow.Evaluator`. So `shadow` never imports `evaluation`; the composition root
wires them with `shadow.WithEvaluator(engine)`. Part 1's `Manager.Shadow` was
extended to carry the primary `Response`/`Latency` (a `Primary` value) so the pair
can be compared on completion.

`Compare` is **pure and deterministic** (`Side × Side → ComparisonResult`) and is
testable without the shadow package.

## 3. Comparison Strategy

**Quality** (agreement, not correctness):
- **Exact Match** — trimmed-equal responses.
- **Text Similarity** — word-frequency **cosine** (deterministic, order-independent,
  frequency-weighted); default, overridable via `WithTextSimilarity`.
- **Embedding Similarity** — an *optional abstraction* (`WithEmbeddingSimilarity`);
  ModelMesh ships no embedding model here. Populated only when a scorer is wired.
- Response length + difference, finish reason + match.

**Latency** — primary vs shadow latency, signed `Difference` (shadow − primary),
`ShadowFaster`.

**Cost** — priced from reported token usage via an injectable `CostModel`
(`budget.PricingModel` fits via `CostModelFunc`); the model that **actually served**
the response (`Response.Model`) is priced. Signed `Difference`, `ShadowCheaper`,
plus token counts and `TokenDifference`.

**Winner** — the more **efficient** side by cost + latency (one point each); a tie
when neither leads. Quality is reported but never decides a winner — similarity
measures *agreement*, not *quality*, so it must not be mistaken for a verdict.

## 4. Evaluation Models

| Type | Role |
|------|------|
| `Side` | one participant (provider, model, response, latency) — neutral `Compare` input |
| `QualityMetrics` | exact match, text/embedding similarity, length, finish reason |
| `LatencyMetrics` | primary/shadow latency + signed difference |
| `CostMetrics` | primary/shadow cost + difference; tokens + difference |
| `ComparisonResult` | the three metric groups + provider/model + `Winner` |
| `EvaluationRecord` | one stored evaluation (id, correlation, timestamp, comparable, error, comparison) |

A failed shadow is stored with `Comparable=false` and its error; it is excluded
from the averages.

## 5. Statistics

`Engine.Statistics()` aggregates the stored records:

| Statistic | Definition |
|-----------|------------|
| **Average Latency Difference** | mean of `shadow − primary` latency over comparable records |
| **Average Cost Difference** | mean of `shadow − primary` cost |
| **Average Token Difference** | mean of `shadow − primary` tokens |
| **Average Similarity** | mean text similarity |
| **Exact Match Rate** | fraction exactly matching |
| **Provider Win Rate** | per provider: efficiency wins / appearances (ties are not wins) |

All deterministic; only successful shadows contribute.

## 6. Configuration & Wiring

```go
engine := evaluation.New(
    evaluation.WithCostModel(evaluation.CostModelFunc(budgetModel.Actual)), // price by usage
    evaluation.WithTextSimilarity(myFn),                                    // optional
    evaluation.WithEmbeddingSimilarity(myEmbedFn),                          // optional abstraction
)
sm, _ := shadow.New(shadow.Config{Policy: "fixed_percentage", Percentage: 5}, pm,
    shadow.WithEvaluator(engine))          // ← the seam
gw := gateway.New(router, cache, cfg, gateway.WithShadow(sm))

// later:
stats := engine.Statistics()
records := engine.Records()
```

## 7. Extension Guide

- **New similarity** — implement `TextSimilarity` (Jaccard, Levenshtein ratio) and
  pass `WithTextSimilarity`; plug embeddings via `WithEmbeddingSimilarity`.
- **Real cost** — inject `budget`'s pricing model via `CostModelFunc`.
- **Durable storage** — the in-memory `store` is the seam a persistent sink or the
  Part 3 report generator reads from (`Records()`), so swapping it is additive.

## 8. Readiness for Part 3 (Reports & Dashboards)

- **Records and statistics are already structured and serializable** (`json` tags
  throughout), so a report generator or metrics exporter consumes them directly.
- **Provider win rate + averages** are the exact aggregates a comparison report or
  dashboard renders; Part 3 adds presentation, not computation.
- **No coupling to undo** — Part 3 adds a report/export stage over `Records()` /
  `Statistics()`; comparison, metrics, and storage are done.
