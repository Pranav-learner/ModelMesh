package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Key prefixes distinguish request kinds so a chat key can never collide with an
// embeddings key.
const (
	chatKeyPrefix      = "chat:"
	embeddingKeyPrefix = "emb:"
)

// KeyGenerator produces deterministic cache keys from unified requests. The key
// incorporates the routed model (routing runs before the cache), so entries are
// keyed per-model. Non-semantic fields (metadata) are excluded so equivalent
// requests share a key.
type KeyGenerator interface {
	ChatKey(model string, req provider.ChatRequest) string
	EmbeddingKey(model string, req provider.EmbeddingRequest) string
}

// SHA256KeyGenerator hashes a canonical JSON representation of the semantically
// relevant request fields with SHA-256. JSON of a fixed-field struct is
// deterministic, so equal requests always hash to the same key.
type SHA256KeyGenerator struct{}

// NewKeyGenerator returns the default SHA-256 key generator.
func NewKeyGenerator() KeyGenerator { return SHA256KeyGenerator{} }

// canonicalChat is the subset of a chat request that affects the response. Field
// order is fixed, so the marshaled form is stable.
type canonicalChat struct {
	Model       string                 `json:"model"`
	Messages    []provider.ChatMessage `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Temperature *float64               `json:"temperature"`
	TopP        *float64               `json:"top_p"`
	Stop        []string               `json:"stop"`
}

// ChatKey returns a stable cache key for a chat request served by model.
func (SHA256KeyGenerator) ChatKey(model string, req provider.ChatRequest) string {
	payload := canonicalChat{
		Model:       model,
		Messages:    req.Messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
	}
	return chatKeyPrefix + hashJSON(payload)
}

// canonicalEmbedding is the subset of an embeddings request that affects the
// response. Input order is preserved because it maps to output order.
type canonicalEmbedding struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingKey returns a stable cache key for an embeddings request served by
// model.
func (SHA256KeyGenerator) EmbeddingKey(model string, req provider.EmbeddingRequest) string {
	payload := canonicalEmbedding{Model: model, Input: req.Input}
	return embeddingKeyPrefix + hashJSON(payload)
}

// hashJSON marshals v to canonical JSON and returns its hex SHA-256. Marshaling a
// fixed-field struct cannot fail for these types, so the error is ignored.
func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
