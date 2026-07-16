package loadbalancer

import "time"

// rollingLatency tracks a rolling average of the most recent latency samples
// using a fixed-size ring buffer with a running sum, so record and average are
// both O(1). It deliberately does not depend on Prometheus or any metrics
// backend: latency-aware selection must work with no observability wired.
//
// It is not safe for concurrent use on its own; the InstanceRegistry guards every
// access with its own lock.
type rollingLatency struct {
	window []time.Duration
	idx    int           // next write position
	count  int           // number of valid samples (<= len(window))
	sum    time.Duration // running sum of the valid samples
}

func newRollingLatency(size int) *rollingLatency {
	if size <= 0 {
		size = DefaultLatencyWindow
	}
	return &rollingLatency{window: make([]time.Duration, size)}
}

// record adds a sample, evicting the oldest once the window is full.
func (r *rollingLatency) record(d time.Duration) {
	if r.count == len(r.window) {
		r.sum -= r.window[r.idx] // evict the oldest sample at the write position
	} else {
		r.count++
	}
	r.window[r.idx] = d
	r.sum += d
	r.idx = (r.idx + 1) % len(r.window)
}

// average returns the mean of the recorded samples, or 0 if none.
func (r *rollingLatency) average() time.Duration {
	if r.count == 0 {
		return 0
	}
	return r.sum / time.Duration(r.count)
}

// samples returns the number of samples currently in the window.
func (r *rollingLatency) samples() int { return r.count }
