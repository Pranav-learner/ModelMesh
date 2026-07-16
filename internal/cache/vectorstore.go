package cache

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

// VectorRecord is a stored vector with its opaque payload and optional expiry.
type VectorRecord struct {
	ID        string
	Vector    []float32
	Payload   []byte
	ExpiresAt time.Time
}

// VectorMatch is a search result: the record's id, its similarity to the query
// (cosine, in [-1,1]; 1 = identical), and its payload.
type VectorMatch struct {
	ID      string
	Score   float64
	Payload []byte
}

// VectorStore is the abstraction backing the semantic cache: it stores vectors
// and searches them by similarity. The in-memory implementation ships here; a
// Redis/ANN-backed store implements the same interface without changing the
// semantic cache.
type VectorStore interface {
	// Add stores (or replaces) a record by ID.
	Add(ctx context.Context, record VectorRecord) error
	// Search returns up to topK records most similar to vector, best first,
	// excluding expired records.
	Search(ctx context.Context, vector []float32, topK int) ([]VectorMatch, error)
	// Delete removes a record by ID.
	Delete(ctx context.Context, id string) error
	// Clear removes all records.
	Clear(ctx context.Context) error
	// Len returns the number of stored (including possibly expired) records.
	Len() int
}

// MemoryVectorStore is a thread-safe, brute-force in-memory VectorStore. It scans
// all records per search (O(n·d)), which is appropriate for an L1-scale semantic
// cache; an approximate-nearest-neighbor backend can replace it behind the
// interface for larger corpora.
type MemoryVectorStore struct {
	mu      sync.RWMutex
	records map[string]VectorRecord
	clock   func() time.Time
}

// NewMemoryVectorStore returns an empty in-memory vector store.
func NewMemoryVectorStore() *MemoryVectorStore {
	return &MemoryVectorStore{records: make(map[string]VectorRecord), clock: time.Now}
}

// Add stores or replaces a record.
func (s *MemoryVectorStore) Add(ctx context.Context, record VectorRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
	return nil
}

// Search returns the topK most-similar non-expired records, best first.
func (s *MemoryVectorStore) Search(ctx context.Context, vector []float32, topK int) ([]VectorMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if topK <= 0 {
		return nil, nil
	}
	now := s.now()

	s.mu.RLock()
	matches := make([]VectorMatch, 0, len(s.records))
	for _, rec := range s.records {
		if !rec.ExpiresAt.IsZero() && !now.Before(rec.ExpiresAt) {
			continue // expired
		}
		matches = append(matches, VectorMatch{
			ID:      rec.ID,
			Score:   cosineSimilarity(vector, rec.Vector),
			Payload: rec.Payload,
		})
	}
	s.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if len(matches) > topK {
		matches = matches[:topK]
	}
	return matches, nil
}

// Delete removes a record by ID.
func (s *MemoryVectorStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

// Clear removes all records.
func (s *MemoryVectorStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = make(map[string]VectorRecord)
	return nil
}

// Len returns the number of stored records.
func (s *MemoryVectorStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

func (s *MemoryVectorStore) now() time.Time { return s.clock() }

// cosineSimilarity returns the cosine similarity of two equal-length vectors, or
// 0 for a length mismatch or a zero-norm vector.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
