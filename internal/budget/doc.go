// Package budget is ModelMesh's financial control plane. It decides, before a
// request is dispatched to a provider, whether the request can be afforded under
// the configured per-user and per-team daily limits, and records what completed
// requests actually cost.
//
// The engine is provider-agnostic: it reasons about models, tokens, and prices,
// never about a specific vendor. It composes three collaborators behind a single
// Manager façade:
//
//   - CostModel — estimates a request's cost before the call (from pricing +
//     token estimates) and computes the actual cost after (from reported usage).
//   - Budget window counters — per-scope (user/team) daily spend, auto-resetting
//     at the window boundary.
//   - Policy — the swappable enforcement strategy applied when a limit would be
//     breached (Reject or Downgrade; Queue / Notify / Approval are reserved).
//
// # Authorize / Commit split
//
// Authorization and commitment are deliberately decoupled, matching the request
// lifecycle: a cache hit never reaches a provider and so never commits.
//
//	Authorize(req)  → estimate cost, read remaining, apply policy → Decision   (no mutation)
//	  ... dispatch to provider (only on a cache miss) ...
//	Commit(record)  → add actual cost to the scope's daily counter             (atomic mutation)
//
// Authorize does not reserve funds; it is a fast read. Commit is the single
// mutation point and is safe under concurrency. This accepts a small, bounded
// overshoot window in exchange for a lock-light hot path — the same tradeoff the
// Budget Engine design documents.
//
// # Decision pipeline
//
//	Request → Estimate Cost → Check Budget → enough? ─ yes → Allow
//	                                            │
//	                                            no → Reject  OR  Downgrade(cheaper model)
package budget
