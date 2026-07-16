package cache

import (
	"context"
	"io"
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
	levels []Cache
	stats  *Stats
	log    logger.Logger
	clock  func() time.Time
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
		m.backfill(ctx, i, entry)
		m.stats.Hit()
		return entry, true, nil
	}
	m.stats.Miss()
	return Entry{}, false, nil
}

// backfill populates faster levels (0..i-1) with a hit found at level i,
// preserving its remaining TTL. Failures are best-effort and logged.
func (m *Manager) backfill(ctx context.Context, hitLevel int, entry Entry) {
	if hitLevel == 0 {
		return
	}
	ttl := entry.RemainingTTL(m.clock())
	for j := 0; j < hitLevel; j++ {
		if err := m.levels[j].Set(ctx, entry.Key, entry.Value, ttl); err != nil {
			m.log.Warn("cache backfill failed",
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

// Clear clears every level.
func (m *Manager) Clear(ctx context.Context) error {
	var firstErr error
	for _, level := range m.levels {
		if err := level.Clear(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close closes every level that implements io.Closer (e.g. stopping janitors).
func (m *Manager) Close() error {
	var firstErr error
	for _, level := range m.levels {
		if closer, ok := level.(io.Closer); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ManagerStats is the aggregated statistics view: overall read-through counters
// plus per-level statistics for levels that report them.
type ManagerStats struct {
	Overall StatsSnapshot            `json:"overall"`
	Levels  map[string]StatsSnapshot `json:"levels"`
}

// Stats returns the composite statistics.
func (m *Manager) Stats() ManagerStats {
	levels := make(map[string]StatsSnapshot, len(m.levels))
	for _, level := range m.levels {
		if reporter, ok := level.(StatsReporter); ok {
			levels[level.Name()] = reporter.Stats()
		}
	}
	return ManagerStats{Overall: m.stats.Snapshot(), Levels: levels}
}
