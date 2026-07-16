package cache

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
)

// Compile-time assertion that Manager satisfies the Cache facade.
var _ Cache = (*Manager)(nil)

// Manager composes one or more cache levels into a single read-through /
// write-through cache. It presents the same Cache interface as a level (a
// facade), so callers are unaware of how many levels exist.
//
// In Phase 3 Part 1 there is a single level (L1). The read-through traversal and
// backfill are already implemented so that adding L2/L3 later requires no change
// here: on a hit at level i, faster levels 0..i-1 are backfilled with the entry's
// remaining TTL.
type Manager struct {
	levels   []Cache       // exact-match levels, fastest first (L1, L2)
	semantic SemanticCache // optional L3 semantic level
	stats    *Stats
	log      logger.Logger
	clock    func() time.Time
	write    WritePolicy

	lookupNanos atomic.Int64   // total time spent in Lookup (for the average)
	asyncWrites sync.WaitGroup // tracks in-flight async populations, drained on Close
}

// Query describes a cache lookup across all levels. Key is the exact-match key
// used by L1/L2; Text and Model drive the semantic L3 level. An empty Text
// disables the semantic level for that request.
type Query struct {
	Key   string
	Model string
	Text  string
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) ManagerOption {
	return func(m *Manager) {
		if l != nil {
			m.log = l
		}
	}
}

// WithSemantic attaches the optional L3 semantic level.
func WithSemantic(s SemanticCache) ManagerOption {
	return func(m *Manager) { m.semantic = s }
}

// WithWritePolicy sets the cache write policy (which levels to populate on Store,
// and whether to populate asynchronously).
func WithWritePolicy(p WritePolicy) ManagerOption {
	return func(m *Manager) { m.write = p }
}

// WithManagerClock injects a time source (for TTL math in backfill).
func WithManagerClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// NewManager constructs a cache Manager over the given ordered levels (fastest
// first). It may be called with zero levels (a no-op cache), which is useful when
// caching is disabled.
func NewManager(levels []Cache, opts ...ManagerOption) *Manager {
	m := &Manager{
		levels: levels,
		stats:  NewStats(),
		log:    logger.Nop(),
		clock:  time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name identifies the composite cache.
func (m *Manager) Name() string { return "manager" }

// Get performs a read-through lookup: it queries each level in order and returns
// the first hit, backfilling faster levels. A level error is logged and treated
// as a miss for that level (fail-safe), so one broken level never fails a lookup.
func (m *Manager) Get(ctx context.Context, key string) (Entry, bool, error) {
	for i, level := range m.levels {
		entry, found, err := level.Get(ctx, key)
		if err != nil {
			m.log.Warn("cache level get failed; treating as miss",
				logger.String("level", level.Name()), logger.Err(err))
			continue
		}
		if !found {
			continue
		}
		entry.Level = level.Name()
		m.promoteTo(ctx, i, key, entry)
		m.stats.Hit()
		return entry, true, nil
	}
	m.stats.Miss()
	return Entry{}, false, nil
}

// Lookup performs the full multi-level lookup in order L1 -> L2 -> L3. Exact
// levels are tried first by key; on a miss, the semantic level is consulted by
// text/model. A hit at any level promotes the entry to all faster levels, so a
// repeat request is served from the fastest cache. A level error is logged and
// treated as a miss (fail-safe).
func (m *Manager) Lookup(ctx context.Context, q Query) (Entry, bool, error) {
	start := m.clock()
	defer func() { m.lookupNanos.Add(int64(m.clock().Sub(start))) }()

	// Exact levels (L1, L2).
	for i, level := range m.levels {
		entry, found, err := level.Get(ctx, q.Key)
		if err != nil {
			m.log.Warn("cache level get failed; treating as miss",
				logger.String("level", level.Name()), logger.Err(err))
			continue
		}
		if !found {
			continue
		}
		entry.Level = level.Name()
		m.promoteTo(ctx, i, q.Key, entry)
		m.stats.Hit()
		return entry, true, nil
	}

	// Semantic level (L3).
	if m.semantic != nil && q.Text != "" {
		entry, found, err := m.semantic.Lookup(ctx, q.Text, q.Model)
		if err != nil {
			m.log.Warn("semantic cache lookup failed; treating as miss", logger.Err(err))
		} else if found {
			entry.Level = m.semantic.Name()
			// Promote a semantic hit into every exact level so the next identical
			// request is an exact hit.
			m.promoteTo(ctx, len(m.levels), q.Key, entry)
			m.stats.Hit()
			return entry, true, nil
		}
	}

	m.stats.Miss()
	return Entry{}, false, nil
}

// Store populates the cache according to the write policy. Exact levels are keyed
// by q.Key; the semantic level indexes q.Text under q.Model. Population is
// best-effort; when synchronous, the first level error is returned.
//
// With an async write policy, population runs in the background (detached from the
// request context) so the request path is not blocked by cache writes; in-flight
// async writes are drained by Close.
func (m *Manager) Store(ctx context.Context, q Query, value []byte, ttl time.Duration) error {
	m.stats.Set()

	if m.write.Async {
		m.asyncWrites.Add(1)
		go func() {
			defer m.asyncWrites.Done()
			_ = m.populate(context.Background(), q, value, ttl)
		}()
		return nil
	}
	return m.populate(ctx, q, value, ttl)
}

// populate performs the actual writes honoring the per-level write policy.
func (m *Manager) populate(ctx context.Context, q Query, value []byte, ttl time.Duration) error {
	var firstErr error
	for _, level := range m.levels {
		if !m.write.writes(level.Name()) {
			continue
		}
		if err := level.Set(ctx, q.Key, value, ttl); err != nil {
			m.log.Warn("cache level set failed", logger.String("level", level.Name()), logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if m.semantic != nil && q.Text != "" && m.write.writes(m.semantic.Name()) {
		if err := m.semantic.Store(ctx, q.Text, q.Model, value, ttl); err != nil {
			m.log.Warn("semantic cache store failed", logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// promoteTo backfills exact levels 0..upTo-1 with an entry, preserving its
// remaining TTL. It is used both for read-through backfill (a hit at exact level
// i backfills 0..i-1) and semantic promotion (upTo = len(levels)).
func (m *Manager) promoteTo(ctx context.Context, upTo int, key string, entry Entry) {
	if upTo <= 0 {
		return
	}
	ttl := entry.RemainingTTL(m.clock())
	for j := 0; j < upTo && j < len(m.levels); j++ {
		if err := m.levels[j].Set(ctx, key, entry.Value, ttl); err != nil {
			m.log.Warn("cache promotion failed",
				logger.String("level", m.levels[j].Name()), logger.Err(err))
		}
	}
}

// Set writes through to every level. It records one logical store and returns the
// first level error (if any); the caller may treat population as best-effort.
func (m *Manager) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	var firstErr error
	for _, level := range m.levels {
		if err := level.Set(ctx, key, value, ttl); err != nil {
			m.log.Warn("cache level set failed",
				logger.String("level", level.Name()), logger.Err(err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	m.stats.Set()
	return firstErr
}

// Delete removes key from every level.
func (m *Manager) Delete(ctx context.Context, key string) error {
	var firstErr error
	for _, level := range m.levels {
		if err := level.Delete(ctx, key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.stats.Delete()
	return firstErr
}

// Exists reports whether any level holds a non-expired entry for key.
func (m *Manager) Exists(ctx context.Context, key string) (bool, error) {
	for _, level := range m.levels {
		ok, err := level.Exists(ctx, key)
		if err != nil {
			continue
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Clear clears every level, including the semantic level.
func (m *Manager) Clear(ctx context.Context) error {
	var firstErr error
	for _, level := range m.levels {
		if err := level.Clear(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if m.semantic != nil {
		if err := m.semantic.Clear(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close drains any in-flight async cache writes, then closes every level (and the
// semantic level) that implements io.Closer.
func (m *Manager) Close() error {
	m.asyncWrites.Wait()
	var firstErr error
	closeIfCloser := func(v any) {
		if closer, ok := v.(io.Closer); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, level := range m.levels {
		closeIfCloser(level)
	}
	if m.semantic != nil {
		closeIfCloser(m.semantic)
	}
	return firstErr
}

// ManagerStats is the aggregated analytics view: overall read-through counters,
// per-level statistics, per-source hit counts and rates, average lookup time, and
// average semantic similarity. Token/cost savings are tracked at the gateway
// (which decodes the cached responses), not here.
type ManagerStats struct {
	Overall StatsSnapshot            `json:"overall"`
	Levels  map[string]StatsSnapshot `json:"levels"`

	MemoryHits   int64 `json:"memory_hits"`
	RedisHits    int64 `json:"redis_hits"`
	SemanticHits int64 `json:"semantic_hits"`
	Misses       int64 `json:"misses"`

	HitRatio        float64 `json:"hit_ratio"`
	MemoryHitRate   float64 `json:"memory_hit_rate"`
	RedisHitRate    float64 `json:"redis_hit_rate"`
	SemanticHitRate float64 `json:"semantic_hit_rate"`

	AverageLookupTime time.Duration `json:"average_lookup_time"`
	AverageSimilarity float64       `json:"average_similarity"`
}

// Stats returns the composite analytics. Per-source hit counts come from each
// level's own statistics (a level records hits as it serves them); rates are per
// total lookups; average lookup time and similarity are Manager/L3 aggregates.
func (m *Manager) Stats() ManagerStats {
	levels := make(map[string]StatsSnapshot, len(m.levels)+1)
	for _, level := range m.levels {
		if reporter, ok := level.(StatsReporter); ok {
			levels[level.Name()] = reporter.Stats()
		}
	}
	if reporter, ok := m.semantic.(StatsReporter); ok {
		levels[m.semantic.Name()] = reporter.Stats()
	}

	overall := m.stats.Snapshot()
	lookups := overall.Lookups

	stats := ManagerStats{
		Overall:           overall,
		Levels:            levels,
		MemoryHits:        levels[LevelL1].Hits,
		RedisHits:         levels[LevelL2].Hits,
		SemanticHits:      levels[LevelL3].Hits,
		Misses:            overall.Misses,
		HitRatio:          overall.HitRatio,
		AverageSimilarity: levels[LevelL3].AvgSimilarity,
	}
	if lookups > 0 {
		stats.MemoryHitRate = float64(stats.MemoryHits) / float64(lookups)
		stats.RedisHitRate = float64(stats.RedisHits) / float64(lookups)
		stats.SemanticHitRate = float64(stats.SemanticHits) / float64(lookups)
		stats.AverageLookupTime = time.Duration(m.lookupNanos.Load() / lookups)
	}
	return stats
}
