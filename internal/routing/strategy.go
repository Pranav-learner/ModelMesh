package routing

import "context"

// Strategy is the pluggable routing algorithm contract. A strategy takes the
// eligible candidates the router enumerated and returns them in priority order
// (best first). It is deliberately small — a single Rank method plus a Name — so
// that new strategies (round-robin, random, cost-first, latency-first, ...) are
// cheap to add and easy to test.
//
// A strategy must be pure with respect to its inputs: given the same context and
// candidates it should return a deterministic ordering (subject to any explicit
// randomness a strategy documents). It performs no provider I/O; candidate
// enumeration is the router's job.
type Strategy interface {
	// Name returns the stable strategy identifier (e.g. "weighted").
	Name() string

	// Rank orders candidates from most to least preferred. It may annotate each
	// candidate (weight, score, reason) but must not add or drop providers beyond
	// what the router supplied. Returning an empty slice yields ErrNoCandidates.
	Rank(ctx context.Context, rc RoutingContext, candidates []Candidate) ([]Candidate, error)
}

// Explainer is an OPTIONAL interface a Strategy may implement to expose the
// normalized scoring-factor weights it used, so the router can include them in
// the routing explanation. Strategies that do not score need not implement it.
type Explainer interface {
	// NormalizedWeights returns the factor weights (summing to 1) used to combine
	// per-factor scores.
	NormalizedWeights() map[string]float64
}
