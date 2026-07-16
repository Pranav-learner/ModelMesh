package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"
	"time"
)

// Compile-time assertions.
var (
	_ SemanticCache = (*SemanticCacheImpl)(nil)
	_ StatsReporter = (*SemanticCacheImpl)(nil)
)

// SemanticCache is the L3 contract. Unlike the exact Cache interface (which is
// keyed by an opaque hash), the semantic cache is keyed by the request's text and
// model, because matching is by embedding similarity, not exact equality.
type SemanticCache interface {
	// Name returns the level identifier (LevelL3).
	Name() string
	// Lookup returns a cached response for a semantically-similar prior request of
	// the same model, if one exists above the similarity threshold.
	Lookup(ctx context.Context, text, model string) (Entry, bool, error)
	// Store indexes value under the embedding of text for the given model.
	Store(ctx context.Context, text, model string, value []byte, ttl time.Duration) error
	// Clear removes all semantic entries.
	Clear(ctx context.Context) error
}

// semanticPayload is what the vector store holds per record: the model the
// response was produced by, the response bytes, and TTL metadata. The model is
// stored so a lookup only reuses a response produced by the same model.
type semanticPayload struct {
	Model     string    `json:"model"`
	Value     []byte    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// SemanticCacheImpl implements the semantic pipeline:
//
//	text -> embedder -> vector search -> similarity -> threshold -> hit
//
// It is decoupled from any embedding model (via Embedder) and any vector backend
// (via VectorStore); both are injected.
type SemanticCacheImpl struct {
	embedder   Embedder
	store      VectorStore
	threshold  float64
	topK       int
	defaultTTL time.Duration
	clock      func() time.Time
	stats      *Stats
	// simSumMicros accumulates the similarity of hits (score * 1e6) so the mean
	// similarity can be reported lock-free.
	simSumMicros atomic.Int64
}

// SemanticOption configures a SemanticCacheImpl.
type SemanticOption func(*SemanticCacheImpl)

// WithSemanticClock injects a time source for deterministic expiry in tests.
func WithSemanticClock(now func() time.Time) SemanticOption {
	return func(s *SemanticCacheImpl) {
		if now != nil {
			s.clock = now
		}
	}
}

// NewSemanticCache constructs the L3 semantic cache from config and injected
// dependencies. A nil embedder defaults to a HashingEmbedder; a nil store to a
// MemoryVectorStore.
func NewSemanticCache(cfg SemanticConfig, embedder Embedder, store VectorStore, opts ...SemanticOption) *SemanticCacheImpl {
	cfg = cfg.withDefaults()
	if embedder == nil {
		embedder = NewHashingEmbedder(cfg.EmbeddingDims)
	}
	if store == nil {
		store = NewMemoryVectorStore()
	}
	s := &SemanticCacheImpl{
		embedder:   embedder,
		store:      store,
		threshold:  cfg.Threshold,
		topK:       cfg.TopK,
		defaultTTL: cfg.DefaultTTL,
		clock:      time.Now,
		stats:      NewStats(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name returns the level identifier ("l3").
func (s *SemanticCacheImpl) Name() string { return LevelL3 }

// Lookup embeds text, searches for the nearest neighbors, and returns the best
// match whose similarity is at or above the threshold and whose model matches. A
// below-threshold or model-mismatched result is a miss, never a wrong answer.
func (s *SemanticCacheImpl) Lookup(ctx context.Context, text, model string) (Entry, bool, error) {
	vector, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return Entry{}, false, err
	}
	matches, err := s.store.Search(ctx, vector, s.topK)
	if err != nil {
		return Entry{}, false, err
	}

	now := s.clock()
	for _, m := range matches {
		if m.Score < s.threshold {
			break // matches are sorted descending; none below can qualify
		}
		var p semanticPayload
		if json.Unmarshal(m.Payload, &p) != nil {
			continue
		}
		if p.Model != model {
			continue // only reuse a response produced by the same model
		}
		if !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) {
			continue // expired
		}
		s.stats.Hit()
		s.simSumMicros.Add(int64(m.Score * 1e6))
		return Entry{
			Key:        m.ID,
			Value:      p.Value,
			CreatedAt:  p.CreatedAt,
			ExpiresAt:  p.ExpiresAt,
			Similarity: m.Score,
		}, true, nil
	}

	s.stats.Miss()
	return Entry{}, false, nil
}

// Store embeds text and indexes value under it for the given model.
func (s *SemanticCacheImpl) Store(ctx context.Context, text, model string, value []byte, ttl time.Duration) error {
	vector, err := s.embedder.Embed(ctx, text)
	if err != nil {
		return err
	}
	now := s.clock()
	_, expiresAt := resolveExpiry(now, ttl, s.defaultTTL)

	payload, err := json.Marshal(semanticPayload{
		Model:     model,
		Value:     value,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return err
	}

	if err := s.store.Add(ctx, VectorRecord{
		ID:        semanticID(model, text),
		Vector:    vector,
		Payload:   payload,
		ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	s.stats.Set()
	return nil
}

// Clear removes all semantic entries.
func (s *SemanticCacheImpl) Clear(ctx context.Context) error {
	return s.store.Clear(ctx)
}

// Stats returns the level's statistics, including the current entry count and
// the mean similarity of hits.
func (s *SemanticCacheImpl) Stats() StatsSnapshot {
	snap := s.stats.Snapshot()
	snap.Entries = s.store.Len()
	if snap.Hits > 0 {
		snap.AvgSimilarity = (float64(s.simSumMicros.Load()) / 1e6) / float64(snap.Hits)
	}
	return snap
}

// semanticID is a stable record id derived from model and text, so re-storing the
// same request replaces rather than duplicates.
func semanticID(model, text string) string {
	sum := sha256.Sum256([]byte(model + "\x00" + text))
	return hex.EncodeToString(sum[:])
}
