package cache

import "sync/atomic"

// Stats is a concurrency-safe set of cache counters. It uses atomics so hot-path
// recording never blocks. It is shared by the memory cache (per-level stats) and
// the Manager (overall stats).
type Stats struct {
	hits      atomic.Int64
	misses    atomic.Int64
	sets      atomic.Int64
	deletes   atomic.Int64
	evictions atomic.Int64
}

// NewStats returns a zeroed Stats.
func NewStats() *Stats { return &Stats{} }

// Hit records a cache hit.
func (s *Stats) Hit() { s.hits.Add(1) }

// Miss records a cache miss.
func (s *Stats) Miss() { s.misses.Add(1) }

// Set records a store.
func (s *Stats) Set() { s.sets.Add(1) }

// Delete records an explicit deletion.
func (s *Stats) Delete() { s.deletes.Add(1) }

// Evict records n evictions (expiry or capacity).
func (s *Stats) Evict(n int64) {
	if n > 0 {
		s.evictions.Add(n)
	}
}

// StatsSnapshot is an immutable view of cache counters plus derived metrics.
type StatsSnapshot struct {
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	Lookups   int64   `json:"lookups"`
	HitRatio  float64 `json:"hit_ratio"`
	Sets      int64   `json:"sets"`
	Deletes   int64   `json:"deletes"`
	Evictions int64   `json:"evictions"`
	// Entries is the current number of stored items, set by levels that track it.
	Entries int `json:"entries"`
}

// Snapshot returns the current counters with derived lookups and hit ratio.
func (s *Stats) Snapshot() StatsSnapshot {
	hits := s.hits.Load()
	misses := s.misses.Load()
	lookups := hits + misses
	var ratio float64
	if lookups > 0 {
		ratio = float64(hits) / float64(lookups)
	}
	return StatsSnapshot{
		Hits:      hits,
		Misses:    misses,
		Lookups:   lookups,
		HitRatio:  ratio,
		Sets:      s.sets.Load(),
		Deletes:   s.deletes.Load(),
		Evictions: s.evictions.Load(),
	}
}
