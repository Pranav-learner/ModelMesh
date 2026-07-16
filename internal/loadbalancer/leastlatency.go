package loadbalancer

import "context"

// LeastLatency selects the instance with the lowest rolling average latency,
// steering traffic toward the fastest-responding instances.
//
// Instances with no latency samples yet are preferred first, so every instance is
// exercised at least once before the strategy exploits the measurements (explore
// then exploit); this also prevents a newly-registered instance from being
// starved by slower incumbents. Ties are broken by fewer requests served, then by
// ID, keeping selection deterministic.
type LeastLatency struct{}

// NewLeastLatency returns a least-latency strategy.
func NewLeastLatency() *LeastLatency { return &LeastLatency{} }

// Name returns the strategy identifier.
func (l *LeastLatency) Name() string { return StrategyLeastLatency }

// Pick returns the candidate with the lowest average latency (unmeasured first).
func (l *LeastLatency) Pick(_ context.Context, _ Request, candidates []Candidate) (Candidate, error) {
	if len(candidates) == 0 {
		return Candidate{}, ErrNoInstances
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if preferred(c.Stats, best.Stats) {
			best = c
		}
	}
	return best, nil
}

// preferred reports whether candidate a should be chosen over b.
func preferred(a, b InstanceStats) bool {
	// Unmeasured instances are explored first.
	aUnmeasured, bUnmeasured := a.Samples == 0, b.Samples == 0
	if aUnmeasured != bUnmeasured {
		return aUnmeasured
	}
	if a.AverageLatency != b.AverageLatency {
		return a.AverageLatency < b.AverageLatency
	}
	if a.RequestCount != b.RequestCount {
		return a.RequestCount < b.RequestCount
	}
	return a.ID < b.ID
}
