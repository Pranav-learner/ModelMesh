package shadow

import "sync"

// Stats is a snapshot of shadow-execution counters.
type Stats struct {
	// Evaluated is the number of requests the manager considered for shadowing.
	Evaluated int64 `json:"evaluated"`
	// Sampled is the number selected by the policy.
	Sampled int64 `json:"sampled"`
	// Dispatched is the number of shadow goroutines launched.
	Dispatched int64 `json:"dispatched"`
	// Completed is the number that finished (success or failure).
	Completed int64 `json:"completed"`
	// Succeeded / Failed break down completions.
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	// Skipped is the number sampled but not dispatched (no secondary available).
	Skipped int64 `json:"skipped"`
}

// tracker accumulates shadow counters and a bounded ring of recent executions. It
// is safe for concurrent use.
type tracker struct {
	mu     sync.Mutex
	stats  Stats
	recent []*ShadowExecution
	max    int
}

func newTracker(max int) *tracker {
	if max <= 0 {
		max = DefaultMaxTrackedExecutions
	}
	return &tracker{max: max}
}

func (t *tracker) evaluated() { t.bump(func(s *Stats) { s.Evaluated++ }) }
func (t *tracker) sampled()   { t.bump(func(s *Stats) { s.Sampled++ }) }
func (t *tracker) skipped()   { t.bump(func(s *Stats) { s.Skipped++ }) }

func (t *tracker) dispatched(exec *ShadowExecution) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.Dispatched++
	t.recent = append(t.recent, exec)
	if len(t.recent) > t.max {
		t.recent = t.recent[len(t.recent)-t.max:]
	}
}

func (t *tracker) completed(success bool) {
	t.bump(func(s *Stats) {
		s.Completed++
		if success {
			s.Succeeded++
		} else {
			s.Failed++
		}
	})
}

func (t *tracker) bump(f func(*Stats)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f(&t.stats)
}

func (t *tracker) snapshot() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

func (t *tracker) recentExecutions() []*ShadowExecution {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*ShadowExecution, len(t.recent))
	copy(out, t.recent)
	return out
}
