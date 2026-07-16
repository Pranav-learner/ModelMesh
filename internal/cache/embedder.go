package cache

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Embedder converts text into a fixed-length vector. It is the abstraction that
// keeps the semantic cache decoupled from any specific embedding model: a real
// provider-backed embedder (e.g. OpenAI text-embedding-3) implements the same
// interface and is injected without changing the semantic cache.
type Embedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// Dimensions returns the vector length the embedder produces.
	Dimensions() int
}

// HashingEmbedder is a deterministic, dependency-free embedder using the feature
// hashing ("hashing trick") technique: tokens are hashed into a fixed number of
// dimensions with a sign to reduce collision bias, then the vector is L2
// normalized. It requires no network and no model download, so it is the default
// for local runs and tests.
//
// It is NOT semantically meaningful (it captures lexical overlap, not meaning);
// it exists so the pipeline is exercisable and deterministic. Inject a real
// embedder in production for true semantic similarity.
type HashingEmbedder struct {
	dims int
}

// NewHashingEmbedder returns a hashing embedder of the given dimensionality
// (defaulting to DefaultEmbeddingDims when non-positive).
func NewHashingEmbedder(dims int) *HashingEmbedder {
	if dims <= 0 {
		dims = DefaultEmbeddingDims
	}
	return &HashingEmbedder{dims: dims}
}

// Dimensions returns the embedder's vector length.
func (e *HashingEmbedder) Dimensions() int { return e.dims }

// Embed hashes the tokens of text into a normalized vector.
func (e *HashingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, e.dims)
	for _, tok := range tokenize(text) {
		idx, sign := hashToken(tok, e.dims)
		vec[idx] += sign
	}
	l2Normalize(vec)
	return vec, nil
}

// tokenize lowercases text and splits it into alphanumeric tokens.
func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// hashToken maps a token to a bucket index and a sign in {-1,+1}.
func hashToken(tok string, dims int) (int, float32) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(tok))
	sum := h.Sum32()
	idx := int(sum % uint32(dims))
	if sum&1 == 0 {
		return idx, 1
	}
	return idx, -1
}

// l2Normalize scales vec to unit length in place (no-op for a zero vector).
func l2Normalize(vec []float32) {
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return
	}
	norm := float32(math.Sqrt(sumSq))
	for i := range vec {
		vec[i] /= norm
	}
}
