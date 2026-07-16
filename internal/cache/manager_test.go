package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// errCache is a level that fails every operation, to test fail-safe behavior.
type errCache struct{ name string }

func (e errCache) Name() string { return e.name }
func (e errCache) Get(context.Context, string) (Entry, bool, error) {
	return Entry{}, false, errors.New("boom")
}
func (e errCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("boom")
}
func (e errCache) Delete(context.Context, string) error         { return errors.New("boom") }
func (e errCache) Exists(context.Context, string) (bool, error) { return false, errors.New("boom") }
func (e errCache) Clear(context.Context) error                  { return errors.New("boom") }

func newL1(t *testing.T) *MemoryCache {
	t.Helper()
	c := NewMemoryCache(MemoryConfig{})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestManager_ReadThroughSingleLevel(t *testing.T) {
	l1 := newL1(t)
	m := NewManager([]Cache{l1})
	ctx := context.Background()

	if _, found, _ := m.Get(ctx, "k"); found {
		t.Errorf("unexpected hit on empty cache")
	}
	_ = m.Set(ctx, "k", []byte("v"), time.Minute)

	e, found, err := m.Get(ctx, "k")
	if err != nil || !found || string(e.Value) != "v" {
		t.Fatalf("Get = %q,%v,%v", e.Value, found, err)
	}
	if e.Level != LevelL1 {
		t.Errorf("served level = %q, want l1", e.Level)
	}
}

func TestManager_Backfill(t *testing.T) {
	l1 := newL1(t)
	l2 := NewMemoryCache(MemoryConfig{})
	l2.name = LevelL2 // relabel so the two levels are distinguishable
	t.Cleanup(func() { _ = l2.Close() })
	m := NewManager([]Cache{l1, l2})
	ctx := context.Background()

	// Seed only L2 directly, so a Get must fall through and backfill L1.
	_ = l2.Set(ctx, "k", []byte("v"), time.Minute)

	e, found, _ := m.Get(ctx, "k")
	if !found || e.Level != LevelL2 {
		t.Fatalf("expected L2 hit, got found=%v level=%q", found, e.Level)
	}
	if ok, _ := l1.Exists(ctx, "k"); !ok {
		t.Errorf("L1 was not backfilled after an L2 hit")
	}
}

func TestManager_LevelErrorIsFailSafe(t *testing.T) {
	l1 := newL1(t)
	// A broken first level must not fail the lookup; the second serves it.
	m := NewManager([]Cache{errCache{name: "broken"}, l1})
	ctx := context.Background()
	_ = l1.Set(ctx, "k", []byte("v"), time.Minute)

	e, found, err := m.Get(ctx, "k")
	if err != nil || !found || string(e.Value) != "v" {
		t.Fatalf("Get through broken level = %q,%v,%v", e.Value, found, err)
	}
}

func TestManager_WriteThrough(t *testing.T) {
	l1 := newL1(t)
	l2 := NewMemoryCache(MemoryConfig{})
	l2.name = LevelL2
	t.Cleanup(func() { _ = l2.Close() })
	m := NewManager([]Cache{l1, l2})
	ctx := context.Background()

	_ = m.Set(ctx, "k", []byte("v"), time.Minute)
	if ok, _ := l1.Exists(ctx, "k"); !ok {
		t.Errorf("write-through missed L1")
	}
	if ok, _ := l2.Exists(ctx, "k"); !ok {
		t.Errorf("write-through missed L2")
	}
}

func TestManager_Stats(t *testing.T) {
	l1 := newL1(t)
	m := NewManager([]Cache{l1})
	ctx := context.Background()

	_ = m.Set(ctx, "k", []byte("v"), time.Minute)
	_, _, _ = m.Get(ctx, "k")    // hit
	_, _, _ = m.Get(ctx, "miss") // miss

	s := m.Stats()
	if s.Overall.Hits != 1 || s.Overall.Misses != 1 {
		t.Errorf("overall stats = %+v", s.Overall)
	}
	if _, ok := s.Levels[LevelL1]; !ok {
		t.Errorf("per-level stats missing L1: %+v", s.Levels)
	}
}

func TestManager_EmptyIsNoop(t *testing.T) {
	m := NewManager(nil)
	ctx := context.Background()
	if _, found, err := m.Get(ctx, "k"); found || err != nil {
		t.Errorf("empty manager Get = found=%v err=%v", found, err)
	}
	if err := m.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Errorf("empty manager Set = %v", err)
	}
}

func TestManager_Close(t *testing.T) {
	l1 := NewMemoryCache(MemoryConfig{CleanupInterval: 10 * time.Millisecond})
	m := NewManager([]Cache{l1})
	if err := m.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	// After close the janitor is stopped; ops on L1 return ErrCacheClosed.
	if _, _, err := l1.Get(context.Background(), "k"); err != ErrCacheClosed {
		t.Errorf("level not closed by manager: %v", err)
	}
}
