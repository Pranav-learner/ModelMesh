package cache

import (
	"fmt"
	"time"
)

// This file provides human-readable diagnostics for cache lookups, to make cache
// behavior easy to inspect and explain. All functions are pure renderers over the
// cache DTOs — no I/O, no state.

// InspectEntry renders the metadata of a cache entry: which layer served it, its
// size, similarity (for semantic hits), and TTL/age relative to now.
func InspectEntry(e Entry, now time.Time) string {
	ttl := "no-expiry"
	if !e.ExpiresAt.IsZero() {
		if remaining := e.RemainingTTL(now); remaining > 0 {
			ttl = remaining.Round(time.Millisecond).String()
		} else {
			ttl = "expired"
		}
	}
	sim := ""
	if e.Level == LevelL3 {
		sim = fmt.Sprintf(" similarity=%.4f", e.Similarity)
	}
	return fmt.Sprintf("layer=%s bytes=%d age=%s ttl=%s%s",
		layerName(e.Level), len(e.Value), e.Age(now).Round(time.Millisecond), ttl, sim)
}

// ExplainHit renders why a lookup hit (or missed), naming the cache layer and, for
// a semantic hit, the similarity score that cleared the threshold.
func ExplainHit(e Entry, found bool) string {
	if !found {
		return "cache miss (no layer served the request)"
	}
	switch e.Level {
	case LevelL1:
		return "L1 memory hit (exact match)"
	case LevelL2:
		return "L2 Redis hit (exact match, fleet-shared)"
	case LevelL3:
		return fmt.Sprintf("L3 semantic hit (cosine similarity %.4f)", e.Similarity)
	default:
		return "cache hit"
	}
}

// LayerUsed returns a friendly name for the layer that served an entry.
func LayerUsed(e Entry) string { return layerName(e.Level) }

func layerName(level string) string {
	switch level {
	case LevelL1:
		return "L1 (memory)"
	case LevelL2:
		return "L2 (redis)"
	case LevelL3:
		return "L3 (semantic)"
	case "":
		return "none"
	default:
		return level
	}
}
