// Package adaptive is ModelMesh's request-aware routing policy: it turns the
// Request Analysis Framework's output (complexity + routing hints) into
// per-request scoring-factor weights that adapt the Routing Engine's decisions.
//
// It is the intelligence layer that connects analysis to routing without either
// depending on the other: analysis emits hints, adaptive maps them to weights, and
// the weighted routing strategy consumes those weights via
// RoutingContext.Attributes (routing.AttrFactorWeights). Both routing and analysis
// stay independent; adaptive is the only package that knows about both.
//
// # Adaptation
//
//	AnalysisResult.Hints ─▶ Weigher.Adapt ─▶ FactorWeights (per request)
//	                                            │
//	  simple  → +cost  −quality                 ▼
//	  complex → +quality −cost        routing.AttrFactorWeights on the context
//	  latency-sensitive → +latency              │
//	  cost-sensitive    → +cost                 ▼
//	  high-context / reasoning → +quality   WeightedStrategy scores with them
//
// The adjustment magnitudes are fully configurable, deterministic, and explainable:
// every weight change records the factor, the before/after values, and a reason.
// The package also exposes resource metrics (classification distribution, average
// complexity, hint usage, weight changes, routing accuracy) and the diagnostics
// that render each stage of a request-aware routing decision.
package adaptive
