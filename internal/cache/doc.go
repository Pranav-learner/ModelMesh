// Package cache implements ModelMesh's caching framework and its L1 (in-memory)
// level.
//
// # Scope (Phase 3 Part 1)
//
// This package establishes the caching architecture that later levels build on:
// the Cache contract every level implements, a read-through/write-through Manager
// that composes levels, the Entry model and key generation, TTL handling, cache
// statistics, and a thread-safe in-memory L1 implementation. Redis (L2) and the
// semantic cache (L3) are later parts and are NOT implemented here; the Manager
// is already structured to compose them behind the same interface.
//
// # Layering
//
// The core cache is a reusable primitive: it depends only on the unified provider
// DTOs (for key generation) and the logger. It does NOT depend on the router.
// The router↔cache integration (the middleware that turns "route + dispatch"
// into "lookup → route+dispatch → populate") lives in the gateway package, so the
// cache stays decoupled from routing.
//
//	Application -> gateway -> Router -> Cache Manager -> L1 (memory) -> Provider
//
// # Extension points
//
//   - New cache levels implement Cache and are added to the Manager's ordered
//     level list; read-through and backfill work unchanged.
//   - Optional StatsReporter and io.Closer interfaces let a level expose stats
//     and participate in shutdown without widening the core Cache contract.
//   - The KeyGenerator interface allows alternative keying (e.g. a future
//     semantic key) without changing callers.
package cache
