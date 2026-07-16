package loadbalancer

import "context"

// Candidate is a read-only view of one eligible instance handed to a Strategy. It
// pairs the immutable descriptor with a snapshot of the instance's runtime stats,
// so a strategy has everything it needs to choose without touching the registry.
type Candidate struct {
	Instance Instance
	Stats    InstanceStats
}

// Strategy is the pluggable selection algorithm contract. Given the eligible
// candidates the balancer enumerated (already filtered to enabled + healthy and
// sorted by ID for determinism), a strategy returns the one that should serve the
// request.
//
// A strategy performs no provider I/O and does not mutate the registry; candidate
// enumeration and feedback are the balancer's job. It may hold its own internal
// state (e.g. a round-robin cursor) and must be safe for concurrent Pick calls.
// Keeping the contract this small is what lets new algorithms — Weighted Round
// Robin, Least Connections, Random, Consistent Hashing — plug in without changing
// the balancer or the registry.
type Strategy interface {
	// Name returns the stable strategy identifier (e.g. "round_robin").
	Name() string
	// Pick chooses one candidate. It must return ErrNoInstances if candidates is
	// empty, and otherwise a candidate drawn from the supplied slice.
	Pick(ctx context.Context, req Request, candidates []Candidate) (Candidate, error)
}
