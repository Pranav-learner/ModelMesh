// Package shadow implements ModelMesh's Shadow Traffic framework: it duplicates a
// small, sampled fraction of requests to an alternative provider for evaluation,
// without ever affecting the primary response the application receives.
//
// Shadow requests are fire-and-forget and fully isolated: they run asynchronously
// on a detached context, their failures and panics are contained, and their
// results are recorded for later evaluation — never returned to the caller.
//
//	Application → Primary Request → Provider A → response to application
//	                     │
//	                     └── (sampled) Shadow Request → Provider B → recorded only
//
// # Design
//
// The Manager is the entry point. For each request it: (1) asks the sampling
// Policy whether to shadow, (2) selects a secondary provider independently of the
// primary via a Selector, (3) clones the request, and (4) dispatches it on a
// background goroutine, tracking execution. The application's response is produced
// entirely by the primary path and is never touched by shadowing.
//
// This part deliberately does not compare responses or generate reports (Part 2);
// it only produces and records ShadowExecutions. The Policy and Selector are
// pluggable so rule-based sampling and smarter secondary selection can be added
// without changing the Manager.
package shadow
