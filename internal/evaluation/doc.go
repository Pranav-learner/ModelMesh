// Package evaluation is ModelMesh's Evaluation Engine: it compares a primary
// response against its shadow counterpart and produces deterministic quality,
// latency, and cost metrics for offline analysis.
//
// It consumes the primary/shadow pairs the Shadow Traffic framework emits
// (implementing shadow.Evaluator), compares them, and stores an EvaluationRecord
// per pair. Aggregate Statistics (average latency/cost difference, average
// similarity, provider win rate) are computed over the stored records.
//
//	shadow.Comparison ─▶ Engine.Evaluate ─▶ ComparisonResult ─▶ EvaluationRecord ─▶ Store
//	                                                                      │
//	                                                            Engine.Statistics()
//
// Similarity is intentionally lightweight and deterministic (exact match + a
// word-frequency cosine), with an optional embedding-similarity abstraction that a
// caller can plug in — this is deliberately not an LLM-as-a-Judge system. Cost is
// computed from reported token usage via an injectable cost model.
//
// The engine never affects production traffic: it runs off the shadow goroutine
// (already off the primary path) and only reads responses that have already been
// produced.
package evaluation
