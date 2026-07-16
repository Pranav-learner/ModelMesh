package cache

import (
	"context"
	"testing"
	"time"
)

// mockEmbedder maps specific texts to specific vectors for precise threshold
// tests, defaulting to a zero vector for unmapped text.
type mockEmbedder struct {
	dims    int
	vectors map[string][]float32
}

func (m mockEmbedder) Dimensions() int { return m.dims }
func (m mockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := m.vectors[text]; ok {
		return v, nil
	}
	return make([]float32, m.dims), nil
}

// --- embedder ---------------------------------------------------------------

func TestHashingEmbedder(t *testing.T) {
	e := NewHashingEmbedder(64)
	if e.Dimensions() != 64 {
		t.Errorf("Dimensions = %d, want 64", e.Dimensions())
	}
	a, _ := e.Embed(context.Background(), "hello world")
	b, _ := e.Embed(context.Background(), "hello world")
	// Deterministic.
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("embedding not deterministic at %d", i)
		}
	}
	// Whitespace/punctuation-insensitive tokenization: same tokens -> same vector.
	c, _ := e.Embed(context.Background(), "  hello,  world! ")
	for i := range a {
		if a[i] != c[i] {
			t.Fatalf("tokenization not whitespace-insensitive at %d", i)
		}
	}
	// Different text -> different vector.
	d, _ := e.Embed(context.Background(), "completely different")
	if cosineSimilarity(a, d) > 0.5 {
		t.Errorf("unrelated texts too similar: %v", cosineSimilarity(a, d))
	}
}

// --- vector store -----------------------------------------------------------

func TestMemoryVectorStore(t *testing.T) {
	s := NewMemoryVectorStore()
	ctx := context.Background()
	_ = s.Add(ctx, VectorRecord{ID: "x", Vector: []float32{1, 0}, Payload: []byte("x")})
	_ = s.Add(ctx, VectorRecord{ID: "y", Vector: []float32{0, 1}, Payload: []byte("y")})

	matches, err := s.Search(ctx, []float32{1, 0.1}, 2)
	if err != nil {
		t.Fatalf("Search() = %v", err)
	}
	if len(matches) != 2 || matches[0].ID != "x" {
		t.Errorf("nearest = %+v, want x first", matches)
	}
	if matches[0].Score <= matches[1].Score {
		t.Errorf("matches not sorted by similarity descending")
	}
	if s.Len() != 2 {
		t.Errorf("Len = %d, want 2", s.Len())
	}
	_ = s.Delete(ctx, "x")
	if s.Len() != 1 {
		t.Errorf("Len after Delete = %d, want 1", s.Len())
	}
}

func TestMemoryVectorStore_ExcludesExpired(t *testing.T) {
	clk := newClock()
	s := &MemoryVectorStore{records: map[string]VectorRecord{}, clock: clk.Now}
	ctx := context.Background()
	_ = s.Add(ctx, VectorRecord{ID: "live", Vector: []float32{1, 0}, ExpiresAt: clk.Now().Add(time.Minute)})
	_ = s.Add(ctx, VectorRecord{ID: "dead", Vector: []float32{1, 0}, ExpiresAt: clk.Now().Add(time.Second)})

	clk.Advance(2 * time.Second)
	matches, _ := s.Search(ctx, []float32{1, 0}, 5)
	if len(matches) != 1 || matches[0].ID != "live" {
		t.Errorf("expired record not excluded from search: %+v", matches)
	}
}

func TestCosineSimilarity(t *testing.T) {
	if s := cosineSimilarity([]float32{1, 0}, []float32{1, 0}); s < 0.999 {
		t.Errorf("identical vectors cosine = %v, want ~1", s)
	}
	if s := cosineSimilarity([]float32{1, 0}, []float32{0, 1}); s != 0 {
		t.Errorf("orthogonal cosine = %v, want 0", s)
	}
	if s := cosineSimilarity([]float32{1, 0}, []float32{0, 0}); s != 0 {
		t.Errorf("zero-norm cosine = %v, want 0", s)
	}
	if s := cosineSimilarity([]float32{1}, []float32{1, 2}); s != 0 {
		t.Errorf("mismatched length cosine = %v, want 0", s)
	}
}

// --- semantic cache ---------------------------------------------------------

func newSemantic(t *testing.T, threshold float64, emb Embedder) *SemanticCacheImpl {
	t.Helper()
	return NewSemanticCache(SemanticConfig{Threshold: threshold, TopK: 5, EmbeddingDims: 2, DefaultTTL: time.Minute}, emb, nil)
}

func TestSemantic_HitAboveThreshold(t *testing.T) {
	emb := mockEmbedder{dims: 2, vectors: map[string][]float32{
		"stored":  {1, 0},
		"similar": {0.99, 0.14}, // cosine ~0.99 to "stored"
		"distant": {0, 1},       // cosine 0
	}}
	s := newSemantic(t, 0.9, emb)
	ctx := context.Background()

	if err := s.Store(ctx, "stored", "gpt", []byte("cached"), 0); err != nil {
		t.Fatalf("Store() = %v", err)
	}

	// Similar prompt of the same model -> hit.
	e, found, err := s.Lookup(ctx, "similar", "gpt")
	if err != nil || !found || string(e.Value) != "cached" {
		t.Fatalf("similar lookup = %q,%v,%v, want hit", e.Value, found, err)
	}
	// Distant prompt -> miss.
	if _, found, _ := s.Lookup(ctx, "distant", "gpt"); found {
		t.Errorf("distant prompt produced a hit")
	}
	// Same prompt, different model -> miss (won't reuse another model's response).
	if _, found, _ := s.Lookup(ctx, "similar", "claude"); found {
		t.Errorf("model mismatch produced a hit")
	}
}

func TestSemantic_BelowThresholdMisses(t *testing.T) {
	emb := mockEmbedder{dims: 2, vectors: map[string][]float32{
		"stored": {1, 0},
		"query":  {0.8, 0.6}, // cosine 0.8
	}}
	s := newSemantic(t, 0.9, emb) // threshold above the similarity
	ctx := context.Background()
	_ = s.Store(ctx, "stored", "gpt", []byte("cached"), 0)

	if _, found, _ := s.Lookup(ctx, "query", "gpt"); found {
		t.Errorf("below-threshold similarity produced a hit")
	}
}

func TestSemantic_Expiry(t *testing.T) {
	clk := newClock()
	emb := mockEmbedder{dims: 2, vectors: map[string][]float32{"p": {1, 0}}}
	// The vector store must share the semantic cache's clock so record expiry and
	// entry expiry advance together.
	store := &MemoryVectorStore{records: map[string]VectorRecord{}, clock: clk.Now}
	s := NewSemanticCache(SemanticConfig{Threshold: 0.9, TopK: 5, EmbeddingDims: 2, DefaultTTL: time.Minute},
		emb, store, WithSemanticClock(clk.Now))
	ctx := context.Background()
	_ = s.Store(ctx, "p", "gpt", []byte("v"), time.Minute)

	if _, found, _ := s.Lookup(ctx, "p", "gpt"); !found {
		t.Fatalf("entry missing before expiry")
	}
	clk.Advance(61 * time.Second)
	if _, found, _ := s.Lookup(ctx, "p", "gpt"); found {
		t.Errorf("expired semantic entry returned")
	}
}

func TestSemantic_Stats(t *testing.T) {
	emb := mockEmbedder{dims: 2, vectors: map[string][]float32{"a": {1, 0}}}
	s := newSemantic(t, 0.9, emb)
	ctx := context.Background()
	_ = s.Store(ctx, "a", "gpt", []byte("v"), 0)
	_, _, _ = s.Lookup(ctx, "a", "gpt")        // hit
	_, _, _ = s.Lookup(ctx, "unmapped", "gpt") // miss (zero vector)

	st := s.Stats()
	if st.Hits != 1 || st.Misses != 1 || st.Sets != 1 || st.Entries != 1 {
		t.Errorf("semantic stats = %+v", st)
	}
	if s.Name() != LevelL3 {
		t.Errorf("Name = %q, want l3", s.Name())
	}
}
