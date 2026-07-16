// Package resilience implements ModelMesh's circuit breaker framework — the core
// of the resilience architecture that contains a failing provider so it cannot
// degrade the gateway.
//
// # Scope (Phase 4 Part 1)
//
// This package establishes the framework only: the CircuitBreaker contract, the
// three-state machine with configurable transitions, resilience errors, and a
// per-provider breaker Manager. It deliberately does NOT implement background
// health monitoring, provider probing goroutines, automatic recovery loops, or
// failover — those are later parts and phases. Recovery here is lazy: the
// Open → Half-Open transition happens on the next request (or State query) after
// the cooldown elapses, driven by an injectable clock, never by a background
// timer.
//
// # State machine
//
//	                 failures >= FailureThreshold
//	┌────────┐ ───────────────────────────────────▶ ┌────────┐
//	│ CLOSED │                                       │  OPEN  │
//	└────────┘ ◀───────────────────────────────────  └────────┘
//	     ▲       successes >= SuccessThreshold             │
//	     │                                                 │ OpenTimeout elapsed
//	     │              ┌────────────┐                     │
//	     └──────────────│ HALF-OPEN  │◀────────────────────┘
//	      (successes)   └────────────┘
//	                          │ any failure
//	                          └────────────▶ OPEN
//
// Transition rules:
//
//   - CLOSED → OPEN: FailureThreshold consecutive failures (a success resets the
//     streak).
//   - OPEN → HALF-OPEN: after OpenTimeout has elapsed since opening (lazy, on the
//     next request/State query).
//   - HALF-OPEN → CLOSED: SuccessThreshold successful probe requests.
//   - HALF-OPEN → OPEN: any failed probe request.
//   - HALF-OPEN admits at most HalfOpenMaxRequests concurrent probe requests;
//     excess requests are rejected with ErrHalfOpenLimitReached.
//
// # Layering
//
// The breaker is provider-independent: it guards an arbitrary `func(ctx) error`,
// so it knows nothing about providers, routing, or HTTP. The composition root
// wraps a provider call in Execute; later parts feed the per-provider states into
// the routing engine's health seam.
//
//	Application -> Circuit Breaker -> Provider
package resilience
