// Package analysis is ModelMesh's Request Analysis Framework. It analyzes every
// incoming request before it reaches the Routing Engine and produces a structured
// AnalysisResult that enriches the routing context with forward-compatible
// metadata (token estimates and prompt features) that routing — and later phases
// such as complexity classification — consume.
//
// It is a leaf subsystem: it depends only on the provider request types and the
// logger, never on routing, so routing stays independent and the analyzer is
// reusable and testable in isolation. Enrichment is one-way: the analyzer emits
// an attribute bag; the composition layer merges it into RoutingContext.
//
// # Pipeline
//
//	Incoming Request
//	   ↓  Preprocess       normalize whitespace, strip redundant formatting,
//	   │                   count messages, extract system prompts, measure history
//	   ↓  Feature Extract  modular extractors: length, code, math, structured data
//	   ↓  Token Estimate   lightweight input / expected-output / total tokens
//	   ↓  AnalysisResult   { Preprocessed, Features, Tokens, Hints }
//	   ↓  RoutingContext   via AnalysisResult.Attributes()
//
// This part deliberately contains no complexity classification, adaptive routing,
// or machine learning — only the analysis framework. The heuristic TokenEstimator
// and the extractor set are pluggable so a future phase can replace them without
// changing the engine or its callers.
package analysis
