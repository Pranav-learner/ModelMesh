// Package optimization is ModelMesh's resource-optimization layer: it composes
// the Budget Engine, the Routing Engine, and the Load Balancer into a single
// pre-dispatch pipeline that optimizes both infrastructure cost and traffic
// distribution.
//
// It is the coordinator that turns three independent subsystems into one
// production behavior, without any of them depending on each other:
//
//	OptimizeRequest
//	   ↓  Routing Engine        choose provider + model
//	   ↓  Budget Engine         authorize the estimated cost
//	   │     ├─ reject   → Plan.Rejected (caller must not dispatch)
//	   │     └─ downgrade → re-run routing with the cheaper model
//	   ↓  Load Balancer         choose a concrete provider instance
//	Plan{ Provider, Model, Instance, Budget decision, savings }
//	   ↓  caller dispatches, then Commit(actual usage, latency)
//
// The Optimizer holds narrow references: a Router interface plus an optional
// Budget manager and Load Balancer. Any of the optional collaborators may be nil,
// in which case that stage is skipped — so the same coordinator works for a
// cost-only, balance-only, or fully-optimized deployment.
//
// Resource metrics (budget usage, downgrades, rejects, load distribution,
// instance utilization, estimated savings) are emitted through a ResourceMetrics
// seam with a no-op default, and a combined ResourceUsage snapshot plus Explain*
// diagnostics make every decision inspectable.
package optimization
