package cache

import (
	"context"
	"errors"
	"time"
)

// Level name constants. Each cache level reports one of these as its Name().
const (
	LevelL1 = "l1" // in-memory, per instance
	LevelL2 = "l2" // Redis (Phase 3 Part 2)
	LevelL3 = "l3" // semantic (Phase 3 Part 3)
)

// Sentinel errors for the cache framework.
var (
	// ErrCacheClosed indicates an operation on a cache that has been closed.
	ErrCacheClosed = errors.New("cache closed")
	// ErrInvalidCacheConfig indicates a structurally invalid cache configuration.
	ErrInvalidCacheConfig = errors.New("invalid cache config")
)

// Entry is a single cached item: an opaque serialized payload plus metadata.
// Values are []byte so every cache level (memory now, Redis later) stores an
// identical representation; callers serialize/deserialize at the boundary.
type Entry struct {
	Key       string    `json:"key"`
	Value     []byte    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is the absolute expiry time; the zero value means "never expires".
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// Level records which cache level served this entry. It is populated by the
	// Manager on a hit and is empty for entries as stored within a level.
	Level string `json:"level,omitempty"`
	// Similarity is the cosine similarity of a semantic (L3) hit, in [0,1]. It is
	// 0 for exact (L1/L2) hits and for stored entries.
	Similarity float64 `json:"similarity,omitempty"`
}

// Expired reports whether the entry has expired as of now.
func (e Entry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt)
}

// Age returns how long ago the entry was created.
func (e Entry) Age(now time.Time) time.Duration { return now.Sub(e.CreatedAt) }

// RemainingTTL returns the time until the entry expires, or 0 if it has expired
// or never expires. It is used to preserve TTL when backfilling faster levels.
func (e Entry) RemainingTTL(now time.Time) time.Duration {
	if e.ExpiresAt.IsZero() {
		return 0
	}
	if d := e.ExpiresAt.Sub(now); d > 0 {
		return d
	}
	return 0
}

// Cache is the contract every cache level implements. It is deliberately small so
// levels (memory, Redis, semantic) are cheap to add and uniform to compose.
// Implementations must be safe for concurrent use and should honor ctx for
// cancellation (levels that perform I/O especially).
type Cache interface {
	// Name returns the level identifier (e.g. LevelL1).
	Name() string
	// Get returns the entry for key and whether it was found. A non-nil error
	// indicates a backend failure (the Manager treats it as a miss and continues).
	Get(ctx context.Context, key string) (Entry, bool, error)
	// Set stores value under key with the given TTL. A non-positive ttl means the
	// level's default TTL applies.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes key. Removing a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Exists reports whether a non-expired entry exists for key, without affecting
	// recency.
	Exists(ctx context.Context, key string) (bool, error)
	// Clear removes all entries.
	Clear(ctx context.Context) error
}

// StatsReporter is an OPTIONAL interface a cache level may implement to expose
// its statistics. The Manager surfaces per-level stats for levels that provide it.
type StatsReporter interface {
	Stats() StatsSnapshot
}

// resolveExpiry computes the effective TTL and absolute expiry time for a store,
// shared by every level so TTL semantics are defined once: a non-positive ttl
// inherits defaultTTL; a non-positive result means "never expires" (zero time).
func resolveExpiry(now time.Time, ttl, defaultTTL time.Duration) (effectiveTTL time.Duration, expiresAt time.Time) {
	effectiveTTL = ttl
	if effectiveTTL <= 0 {
		effectiveTTL = defaultTTL
	}
	if effectiveTTL > 0 {
		expiresAt = now.Add(effectiveTTL)
	}
	return effectiveTTL, expiresAt
}
