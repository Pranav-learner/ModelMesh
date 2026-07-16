package loadbalancer

import (
	"context"
	"sync/atomic"
)

// RoundRobin distributes selections evenly by cycling through the eligible
// candidates in order. The candidates are sorted by ID by the balancer, so the
// rotation is deterministic and fair even as instances are added or removed.
//
// The cursor advances once per Pick regardless of the candidate-set size, so the
// distribution stays balanced across the currently-eligible instances.
type RoundRobin struct {
	cursor atomic.Uint64
}

// NewRoundRobin returns a round-robin strategy.
func NewRoundRobin() *RoundRobin { return &RoundRobin{} }

// Name returns the strategy identifier.
func (r *RoundRobin) Name() string { return StrategyRoundRobin }

// Pick returns the next candidate in rotation.
func (r *RoundRobin) Pick(_ context.Context, _ Request, candidates []Candidate) (Candidate, error) {
	if len(candidates) == 0 {
		return Candidate{}, ErrNoInstances
	}
	// Fetch-and-add returns the pre-increment value, so the first pick uses index
	// 0. Unsigned modulo keeps the index in range across overflow.
	n := r.cursor.Add(1) - 1
	return candidates[n%uint64(len(candidates))], nil
}
