package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock for deterministic expiry tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_000_000, 0)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestCache(t *testing.T, cfg MemoryConfig, clk *fakeClock) *MemoryCache {
	t.Helper()
	c := NewMemoryCache(cfg, WithClock(clk.Now))
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func mustGet(t *testing.T, c *MemoryCache, key string) (Entry, bool) {
	t.Helper()
	e, ok, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) = %v", key, err)
	}
	return e, ok
}

func TestMemory_SetGet(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{DefaultTTL: time.Minute}, clk)

	if err := c.Set(context.Background(), "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	e, ok := mustGet(t, c, "k")
	if !ok || string(e.Value) != "v" {
		t.Errorf("Get = %q,%v, want v,true", e.Value, ok)
	}
	if _, ok := mustGet(t, c, "missing"); ok {
		t.Errorf("Get(missing) found, want miss")
	}
}

func TestMemory_Delete(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{}, clk)
	_ = c.Set(context.Background(), "k", []byte("v"), 0)
	if err := c.Delete(context.Background(), "k"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, ok := mustGet(t, c, "k"); ok {
		t.Errorf("entry still present after Delete")
	}
}

func TestMemory_Exists(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{}, clk)
	_ = c.Set(context.Background(), "k", []byte("v"), time.Minute)

	if ok, _ := c.Exists(context.Background(), "k"); !ok {
		t.Errorf("Exists(k) = false, want true")
	}
	if ok, _ := c.Exists(context.Background(), "x"); ok {
		t.Errorf("Exists(x) = true, want false")
	}
	// Expired entry is not "exists".
	clk.Advance(2 * time.Minute)
	if ok, _ := c.Exists(context.Background(), "k"); ok {
		t.Errorf("Exists(expired) = true, want false")
	}
}

func TestMemory_Clear(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{}, clk)
	_ = c.Set(context.Background(), "a", []byte("1"), time.Minute)
	_ = c.Set(context.Background(), "b", []byte("2"), time.Minute)
	if err := c.Clear(context.Background()); err != nil {
		t.Fatalf("Clear() = %v", err)
	}
	if c.Len() != 0 {
		t.Errorf("Len after Clear = %d, want 0", c.Len())
	}
}

func TestMemory_TTLExpiryLazy(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{}, clk)
	_ = c.Set(context.Background(), "k", []byte("v"), time.Minute)

	if _, ok := mustGet(t, c, "k"); !ok {
		t.Fatalf("entry missing before expiry")
	}
	clk.Advance(61 * time.Second)
	if _, ok := mustGet(t, c, "k"); ok {
		t.Errorf("entry returned after expiry")
	}
	if c.Stats().Evictions == 0 {
		t.Errorf("expired lazy-eviction not counted")
	}
}

func TestMemory_DefaultTTLApplied(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{DefaultTTL: 30 * time.Second}, clk)
	_ = c.Set(context.Background(), "k", []byte("v"), 0) // ttl 0 -> default

	clk.Advance(31 * time.Second)
	if _, ok := mustGet(t, c, "k"); ok {
		t.Errorf("entry did not expire under default TTL")
	}
}

func TestMemory_NoExpiryWhenTTLZero(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{DefaultTTL: 0}, clk) // no default TTL
	_ = c.Set(context.Background(), "k", []byte("v"), 0)

	clk.Advance(1000 * time.Hour)
	if _, ok := mustGet(t, c, "k"); !ok {
		t.Errorf("entry expired though no TTL was set")
	}
}

func TestMemory_LRUEviction(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{MaxEntries: 2}, clk)
	ctx := context.Background()

	_ = c.Set(ctx, "a", []byte("1"), 0)
	_ = c.Set(ctx, "b", []byte("2"), 0)
	// Touch "a" so "b" becomes least-recently-used.
	_, _ = mustGet(t, c, "a")
	_ = c.Set(ctx, "c", []byte("3"), 0) // exceeds capacity -> evict LRU ("b")

	if _, ok := mustGet(t, c, "b"); ok {
		t.Errorf("LRU entry 'b' was not evicted")
	}
	if _, ok := mustGet(t, c, "a"); !ok {
		t.Errorf("recently-used 'a' should survive")
	}
	if _, ok := mustGet(t, c, "c"); !ok {
		t.Errorf("newest 'c' should be present")
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2 (bounded)", c.Len())
	}
}

func TestMemory_Cleanup(t *testing.T) {
	clk := newClock()
	c := newTestCache(t, MemoryConfig{}, clk)
	ctx := context.Background()
	_ = c.Set(ctx, "a", []byte("1"), time.Minute)
	_ = c.Set(ctx, "b", []byte("2"), time.Minute)
	_ = c.Set(ctx, "c", []byte("3"), time.Hour)

	clk.Advance(2 * time.Minute) // a,b expired; c not

	removed, err := c.Cleanup(ctx)
	if err != nil {
		t.Fatalf("Cleanup() = %v", err)
	}
	if removed != 2 {
		t.Errorf("Cleanup removed %d, want 2", removed)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
}

func TestMemory_ClosedRejectsOps(t *testing.T) {
	c := NewMemoryCache(MemoryConfig{})
	_ = c.Close()
	if _, _, err := c.Get(context.Background(), "k"); err != ErrCacheClosed {
		t.Errorf("Get after Close = %v, want ErrCacheClosed", err)
	}
	if err := c.Set(context.Background(), "k", nil, 0); err != ErrCacheClosed {
		t.Errorf("Set after Close = %v, want ErrCacheClosed", err)
	}
	// Close is idempotent.
	if err := c.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
}

func TestMemory_ContextCancellation(t *testing.T) {
	c := NewMemoryCache(MemoryConfig{})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Get(ctx, "k"); err == nil {
		t.Errorf("Get(cancelled) = nil, want context error")
	}
}

func TestMemory_Janitor(t *testing.T) {
	// Real-time janitor sweeps expired entries and Close stops it cleanly.
	c := NewMemoryCache(MemoryConfig{DefaultTTL: 5 * time.Millisecond, CleanupInterval: 10 * time.Millisecond})
	_ = c.Set(context.Background(), "k", []byte("v"), 5*time.Millisecond)

	deadline := time.Now().Add(500 * time.Millisecond)
	for c.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Len() != 0 {
		t.Errorf("janitor did not sweep expired entry; Len = %d", c.Len())
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestMemory_ConcurrentAccess(t *testing.T) {
	c := NewMemoryCache(MemoryConfig{MaxEntries: 100})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "k" + itoa(i%10)
			_ = c.Set(ctx, key, []byte("v"), time.Minute)
			_, _, _ = c.Get(ctx, key)
			_, _ = c.Exists(ctx, key)
			if i%7 == 0 {
				_ = c.Delete(ctx, key)
			}
			_ = c.Stats()
		}(i)
	}
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [4]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
