// Package routing implements ModelMesh's routing framework: the decision engine
// that selects which provider and model should serve a request.
//
// # Scope (Phase 2 Part 1)
//
// This package establishes the routing *architecture* — the Router contract, the
// pluggable Strategy abstraction with a registration system, the routing DTOs
// (context, candidates, decision, explanation), and dependency-injected wiring
// onto the completed Provider Layer. It deliberately does NOT compute scores yet:
// the WeightedStrategy is a skeleton that enumerates and orders candidates
// without weighting math, and provider health is not consulted. Scoring, health
// awareness, and additional strategies arrive in later parts.
//
// # Layering
//
// The router sits above the Provider Layer and depends on it only through the
// narrow ProviderSource interface (satisfied by *provider.Manager), so routing
// never reaches into provider internals and stays unit-testable in isolation:
//
//	Application -> Router -> Strategy -> RoutingDecision -> Provider Manager
//
// The router decides *which* provider/model; the application then dispatches the
// actual call through the Provider Manager using the decision. Routing performs
// no I/O against providers beyond static model discovery.
//
// # Extension points
//
//   - New strategies (round-robin, random, cost-first, latency-first, ...)
//     implement Strategy and register a Builder; no existing code changes.
//   - Additional routing signals ride on RoutingContext.Attributes, so inputs
//     such as a future prompt-complexity signal require no contract change.
package routing
