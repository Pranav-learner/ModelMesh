package cache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// Compile-time assertions: MemoryCache is a Cache, reports stats, and closes.
var (
	_ Cache         = (*MemoryCache)(nil)
	_ StatsReporter = (*MemoryCache)(nil)
)

// MemoryCache is the L1 in-memory cache level: a thread-safe, bounded, TTL'd LRU.
//
// It combines a map (O(1) lookup) with a doubly-linked list (O(1) recency
// updates and LRU eviction). A single mutex guards both, because a Get updates
// recency (moving the entry to the front) and therefore mutates shared state;
// this keeps the structure simple and correct. Sharding to reduce contention is a
// documented future optimization.
//
// Expiration is handled two ways: lazily (an expired entry found on access is
// treated as a miss and removed) and proactively (an optional background janitor
// sweeps expired entries on an interval).
type MemoryCache struct {
	name string

	mu    sync.Mutex
	items map[string]*list.Element // key -> element holding *lruItem
	lru   *list.List               // front = most recently used, back = least

	maxEntries      int
	defaultTTL      time.Duration
	cleanupInterval time.Duration
	clock           func() time.Time

	stats *Stats

	closed bool
	stopCh chan struct{}
	doneCh chan struct{}
}

// lruItem is the value stored in each list element.
type lruItem struct {
	entry Entry
}

// MemoryOption configures a MemoryCache.
type MemoryOption func(*MemoryCache)

// WithClock injects a time source for deterministic expiry in tests.
func WithClock(now func() time.Time) MemoryOption {
	return func(m *MemoryCache) {
		if now != nil {
			m.clock = now
		}
	}
}

// NewMemoryCache constructs an L1 cache from cfg. If cfg.CleanupInterval > 0 a
// background janitor is started; call Close to stop it and release resources.
func NewMemoryCache(cfg MemoryConfig, opts ...MemoryOption) *MemoryCache {
	m := &MemoryCache{
		name:            LevelL1,
		items:           make(map[string]*list.Element),
		lru:             list.New(),
		maxEntries:      cfg.MaxEntries,
		defaultTTL:      cfg.DefaultTTL,
		cleanupInterval: cfg.CleanupInterval,
		clock:           time.Now,
		stats:           NewStats(),
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.cleanupInterval > 0 {
		m.startJanitor()
	}
	return m
}

// Name returns the level identifier ("l1").
func (m *MemoryCache) Name() string { return m.name }

func (m *MemoryCache) now() time.Time { return m.clock() }

// Get returns the entry for key, recording a hit or miss. An expired entry is
// removed and reported as a miss. On a hit the entry becomes most-recently-used.
func (m *MemoryCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Entry{}, false, ErrCacheClosed
	}

	el, ok := m.items[key]
	if !ok {
		m.stats.Miss()
		return Entry{}, false, nil
	}
	it := el.Value.(*lruItem)
	if it.entry.Expired(m.now()) {
		m.removeElement(el)
		m.stats.Evict(1)
		m.stats.Miss()
		return Entry{}, false, nil
	}
	m.lru.MoveToFront(el)
	m.stats.Hit()
	return it.entry, true, nil
}

// Set stores value under key with ttl (or the level default when ttl <= 0),
// updating recency and evicting the least-recently-used entry if over capacity.
func (m *MemoryCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrCacheClosed
	}

	now := m.now()
	_, expiresAt := resolveExpiry(now, ttl, m.defaultTTL)
	entry := Entry{Key: key, Value: value, CreatedAt: now, ExpiresAt: expiresAt}

	if el, ok := m.items[key]; ok {
		el.Value.(*lruItem).entry = entry
		m.lru.MoveToFront(el)
	} else {
		m.items[key] = m.lru.PushFront(&lruItem{entry: entry})
		if m.maxEntries > 0 && m.lru.Len() > m.maxEntries {
			m.evictLRU()
		}
	}
	m.stats.Set()
	return nil
}

// Delete removes key if present.
func (m *MemoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrCacheClosed
	}
	if el, ok := m.items[key]; ok {
		m.removeElement(el)
		m.stats.Delete()
	}
	return nil
}

// Exists reports whether a non-expired entry exists for key. It does not affect
// recency (it is a peek).
func (m *MemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false, ErrCacheClosed
	}
	el, ok := m.items[key]
	if !ok {
		return false, nil
	}
	if el.Value.(*lruItem).entry.Expired(m.now()) {
		m.removeElement(el)
		m.stats.Evict(1)
		return false, nil
	}
	return true, nil
}

// Clear removes all entries.
func (m *MemoryCache) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrCacheClosed
	}
	m.items = make(map[string]*list.Element)
	m.lru.Init()
	return nil
}

// Cleanup proactively removes all expired entries and returns the count removed.
// It is invoked by the janitor and may also be called manually.
func (m *MemoryCache) Cleanup(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrCacheClosed
	}
	now := m.now()
	removed := 0
	for _, el := range m.items {
		if el.Value.(*lruItem).entry.Expired(now) {
			m.removeElement(el)
			removed++
		}
	}
	m.stats.Evict(int64(removed))
	return removed, nil
}

// Len returns the current number of stored entries.
func (m *MemoryCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// Stats returns the level's statistics snapshot, including the current entry count.
func (m *MemoryCache) Stats() StatsSnapshot {
	snap := m.stats.Snapshot()
	snap.Entries = m.Len()
	return snap
}

// Close stops the background janitor and marks the cache closed. Subsequent
// operations return ErrCacheClosed. It is safe to call more than once.
func (m *MemoryCache) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	stop := m.stopCh
	done := m.doneCh
	m.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}
	return nil
}

// evictLRU removes the least-recently-used entry. Caller holds the lock.
func (m *MemoryCache) evictLRU() {
	if back := m.lru.Back(); back != nil {
		m.removeElement(back)
		m.stats.Evict(1)
	}
}

// removeElement removes an element from both the list and the map. Caller holds
// the lock.
func (m *MemoryCache) removeElement(el *list.Element) {
	m.lru.Remove(el)
	delete(m.items, el.Value.(*lruItem).entry.Key)
}

// startJanitor launches the background cleanup goroutine.
func (m *MemoryCache) startJanitor() {
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	go func() {
		defer close(m.doneCh)
		ticker := time.NewTicker(m.cleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = m.Cleanup(context.Background())
			case <-m.stopCh:
				return
			}
		}
	}()
}
