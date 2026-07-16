# ModelMesh — Request Analysis Framework (Implementation Guide)

**Status:** Implemented (Phase 7 Part 1 — analysis framework; Part 2 — deterministic complexity classifier + rule engine + routing hints; no ML)
**Document type:** Implementation Guide
**Last updated:** 2026-07-16
**Related:** [Routing Engine](./Routing-Engine.md) · [Resource-Optimization](./Resource-Optimization.md) · [Prompt Complexity Classifier LLD](../03-components/08-prompt-complexity-classifier.md)

---

## 1. Purpose & Placement

The Request Analysis Framework analyzes **every incoming request before it reaches
the Routing Engine** and produces a structured `AnalysisResult` that enriches the
routing context with metadata future routing decisions use. It is the substrate a
Phase 7 Part 2 complexity classifier plugs into — this part builds only the
framework (preprocessing, feature extraction, token estimation), no classification
and no machine learning.

`internal/analysis` is a **leaf subsystem**: it depends only on the provider
request types and the logger, never on routing. Enrichment is one-way — the
analyzer emits an attribute bag; the gateway merges it into the routing context —
so routing stays independent and the analyzer is reusable and unit-testable.

## 2. The Pipeline

```
Incoming Request
   ↓  Preprocess       normalize whitespace, strip redundant formatting,
   │                   count messages, extract system prompts, measure history
   ↓  Feature Extract  modular extractors: length · code · math · structured data
   ↓  Token Estimate   lightweight input / expected-output / total tokens
   ↓  AnalysisResult   { Preprocessed, Features, Tokens, Hints }
   ↓  RoutingContext   via AnalysisResult.Attributes()  (merged by the gateway)
```

The `Engine` composes these stages; each stage is an interface with a default
implementation, so any can be swapped without touching the others.

## 3. Analysis Engine

`analysis.Engine` implements the `Analyzer` interface:

```go
type Analyzer interface {
    Analyze(ctx context.Context, req provider.ChatRequest) AnalysisResult
}
```

`Analyze` never fails — an empty request yields a zero-but-valid result. The engine
runs the preprocessor, every registered `Extractor`, and the `TokenEstimator`, then
derives `RoutingHints`. Construction is option-based (`WithPreprocessor`,
`WithExtractors` / `WithExtractor`, `WithTokenEstimator`, `WithLogger`,
`WithLongContextThreshold`).

## 4. Prompt Preprocessor

`Preprocessor.Process` cleans each message and structures the prompt:

- **Normalize whitespace** — CRLF/CR → LF; trailing whitespace trimmed per line;
  runs of intra-line whitespace collapsed to one space, **leading indentation
  preserved** so code structure survives.
- **Remove redundant formatting** — runs of blank lines collapsed to at most
  `maxBlankLines` (default 1); leading/trailing blank lines trimmed.
- **Count messages** — total plus per-role (user/assistant/system) turn counts.
- **Extract system prompts** — the contents of every system message.
- **Detect conversation length** — message count and, via the length extractor,
  conversation history length.

The output `Preprocessed` carries the normalized messages, a joined `Text` for
feature extraction, and the latest user `Prompt`.

## 5. Feature Extraction (Modular)

Each signal is produced by a small, independent `Extractor` (`Name()` +
`Extract(Preprocessed, *PromptFeatures)`); the engine runs them in order. Adding a
signal is adding an extractor — the models, engine, and existing extractors are
untouched. See the Feature Catalog below.

## 6. Token Estimator

`TokenEstimator` is an interface so a future phase can replace the heuristic with a
real tokenizer. `HeuristicEstimator` estimates **input tokens** from character
count at ~4 chars/token, **expected output tokens** from the request's `MaxTokens`
(or a default when unset), and their sum as **estimated total tokens** — all
deterministic and dependency-free.

## 7. Routing Enrichment

`AnalysisResult.Attributes()` projects the routing hints onto a flat
`map[string]any`. The token keys **deliberately match the routing engine's
recognized keys** (`estimated_input_tokens` / `estimated_output_tokens`), so the
existing cost scorer consumes the analyzed estimates with **no routing change**.
The remaining keys (`has_code`, `has_math`, `has_structured_data`,
`conversation_turns`, `long_context`, `multi_turn`) are forward-compatible signals
for Part 2.

Wiring is additive: `gateway.WithAnalyzer(a)` runs analysis at request entry,
attaches the result to `ChatResult.Analysis`, and merges its attributes into the
routing context (both the simple/failover paths and the optimized path via
`OptimizeRequest.Attributes`). The analyzer is off by default; existing behavior is
unchanged when it is not wired.

## 8. Feature Catalog

| Feature | Field | How it is detected |
|---------|-------|--------------------|
| Prompt Length | `PromptLength` | rune length of the latest user prompt |
| Character Count | `CharCount` | rune length of the full normalized context |
| Word Count | `WordCount` | whitespace-split token count |
| Message Count | `MessageCount` | number of messages |
| Estimated Context Size | `EstimatedContextSize` | = input token estimate |
| Code Detection | `HasCode` | fenced blocks, language keywords, symbol density / indentation |
| Mathematical Content | `HasMath` | LaTeX commands, Unicode math symbols, arithmetic, math vocabulary |
| Structured Data Presence | `HasStructuredData` | JSON, XML/HTML, YAML, CSV, markdown tables |
| Conversation History Length | `ConversationHistoryLength` | messages preceding the current prompt |
| System Prompt Count | `SystemPromptCount` | number of system messages |

Token estimate: `InputTokens`, `ExpectedOutputTokens`, `EstimatedTotalTokens`.
Routing hints: the above distilled + `LongContext`, `MultiTurn`.

## 9. Extension Guide

- **Add a feature** — implement `Extractor`, add its field to `PromptFeatures`, and
  register with `analysis.New(WithExtractor(x))` (or extend `DefaultExtractors`).
  Nothing else changes.
- **Swap token estimation** — implement `TokenEstimator` and pass
  `WithTokenEstimator`; the heuristic is the default the Part-2 tokenizer replaces.
- **Add a routing signal** — add a field to `RoutingHints`, a key in
  `attributes.go`, and populate it in `Engine.buildHints`. If routing (or a
  scorer) reads the new attribute key, the signal flows through with no analyzer
  change.
- **Consume analysis downstream** — read `analysis.FromContext(ctx)` or
  `ChatResult.Analysis`; the result rides the request context from entry.

## 10. Complexity Classification (Part 2)

The pipeline now runs two more stages after feature extraction and token
estimation, both deterministic and explainable (**no ML**):

**Signals** — a flat `Signals` view is built from the features + token estimate
(including two new extractors, `instructions` and `reasoning`, which populate
`InstructionCount` and `ReasoningIndicatorCount`).

**Rule engine + classifier** — `RuleClassifier` runs a configurable `RuleSet`.
Each `Rule` is a pure predicate over `Signals` with a `Weight`; the classifier
sums the weights of the rules that fire and maps the total onto Simple / Medium /
Complex via two thresholds. It records the triggered rules, the features they
read, and a **confidence** (how decisively the score sits within its band, in
[0.5, 1]). Rule sets are the extension unit — `RuleSet.With(...)` composes them and
`ClassifierConfig.RuleSet` swaps them without touching the engine.

**Hint generator** — `RuleHintGenerator` maps the classification + signals to
routing hints: a `PreferredModelTier` (small/standard/large, configurable),
`LatencySensitive` / `CostSensitive` (simple → both, medium → cost), `HighContext`
(large input), `ReasoningIntensive` (complex, math, or enough reasoning cues), and
an optional `PreferredProvider`. Each hint carries a human-readable reason.

### Classification Strategy

| Band | Score (default) | Meaning |
|------|-----------------|---------|
| Simple | `< 1.5` | short, factual, single-step → small tier, latency+cost sensitive |
| Medium | `1.5 – 3.5` | some code/structure/instructions → standard tier, cost sensitive |
| Complex | `≥ 3.5` | code + math + multi-step reasoning / large context → large tier, reasoning-intensive |

Default rule weights: code 1.5, math 1.5, large-context 1.5, sizable-prompt 1.0,
≥3 instructions 1.0 (+1.0 at ≥6), reasoning cue 1.0 (+1.0 at ≥3 for multi-step),
structured data 0.5, long conversation 0.5. All overridable via config.

### Explainability

Every classification exposes the required four facets — **features used**, **rules
triggered** (name + weight + description), **confidence**, and **generated hints**
(with reasons) — structurally on `Classification` and rendered by
`AnalysisResult.Explain()`:

```
Complexity: complex (score 4.5, confidence 65%)
Features used: has_code, has_math, reasoning_indicators
Rules triggered:
  - contains_code (+1.5): prompt contains source code
  - contains_math (+1.5): prompt contains mathematical content
  - reasoning_requested (+1.0): prompt requests reasoning
Generated hints: tier=large, reasoning-intensive
  - complex complexity → large tier
  - reasoning cues / math / complex → reasoning-intensive
```

### Routing Enrichment

`Attributes()` now also projects `complexity`, `preferred_model_tier`,
`latency_sensitive`, `cost_sensitive`, `high_context`, `reasoning_intensive`, and
(when set) `preferred_provider`. The gateway already merges these into the routing
context, so **every request reaches the router with its complexity, hints, and the
underlying signals** — ready for Part 3 to route on.

## 11. Readiness for Part 3 (Adaptive Routing)

- **Hints are on the wire.** `preferred_model_tier`, the sensitivity flags, and
  `complexity` ride `RoutingContext.Attributes` today; Part 3 adds routing rules /
  scorers that read them — no analysis change.
- **Tiers are provider-agnostic.** The classifier recommends a tier, not a model,
  so Part 3 owns the tier→model mapping (per provider/config) cleanly.
- **Confidence is available** for adaptive strategies that want to fall back to
  cost/latency scoring when a classification is borderline.
- **Everything is configurable and deterministic**, so adaptive routing can be
  tuned and A/B-compared without retraining anything.
