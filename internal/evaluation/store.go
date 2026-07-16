package evaluation

import "sync"

// DefaultMaxRecords bounds the retained evaluation records.
const DefaultMaxRecords = 1024

// store is a bounded, concurrency-safe ring of evaluation records.
type store struct {
	mu      sync.Mutex
	records []EvaluationRecord
	max     int
}

func newStore(max int) *store {
	if max <= 0 {
		max = DefaultMaxRecords
	}
	return &store{max: max}
}

func (s *store) add(r EvaluationRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	if len(s.records) > s.max {
		s.records = s.records[len(s.records)-s.max:]
	}
}

func (s *store) all() []EvaluationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]EvaluationRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *store) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}
