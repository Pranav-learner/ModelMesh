package cache

import (
	"errors"
	"testing"
)

func TestConfig_DefaultsAndValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v", err)
	}
	c := Config{}.WithDefaults()
	if c.DefaultTTL != DefaultTTL {
		t.Errorf("DefaultTTL = %v, want %v", c.DefaultTTL, DefaultTTL)
	}
	if c.Memory.DefaultTTL != DefaultTTL {
		t.Errorf("memory DefaultTTL should inherit top-level TTL, got %v", c.Memory.DefaultTTL)
	}
}

func TestConfig_ValidateErrors(t *testing.T) {
	cases := []Config{
		{DefaultTTL: -1},
		{Memory: MemoryConfig{MaxEntries: -1}},
		{Memory: MemoryConfig{DefaultTTL: -1}},
		{Memory: MemoryConfig{CleanupInterval: -1}},
	}
	for i, c := range cases {
		if err := c.Validate(); !errors.Is(err, ErrInvalidCacheConfig) {
			t.Errorf("case %d: Validate() = %v, want ErrInvalidCacheConfig", i, err)
		}
	}
}

func TestStats_Snapshot(t *testing.T) {
	s := NewStats()
	s.Hit()
	s.Hit()
	s.Miss()
	s.Set()
	s.Delete()
	s.Evict(3)

	snap := s.Snapshot()
	if snap.Hits != 2 || snap.Misses != 1 || snap.Lookups != 3 {
		t.Errorf("counters = %+v", snap)
	}
	if snap.HitRatio < 0.66 || snap.HitRatio > 0.67 {
		t.Errorf("HitRatio = %v, want ~0.667", snap.HitRatio)
	}
	if snap.Sets != 1 || snap.Deletes != 1 || snap.Evictions != 3 {
		t.Errorf("counters = %+v", snap)
	}
}

func TestStats_EmptyHitRatio(t *testing.T) {
	if r := NewStats().Snapshot().HitRatio; r != 0 {
		t.Errorf("empty HitRatio = %v, want 0 (no NaN)", r)
	}
}

func TestStats_EvictIgnoresNonPositive(t *testing.T) {
	s := NewStats()
	s.Evict(0)
	s.Evict(-5)
	if s.Snapshot().Evictions != 0 {
		t.Errorf("non-positive evictions should be ignored")
	}
}
