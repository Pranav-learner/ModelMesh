package anthropic

import "github.com/symbiotes/modelmesh/internal/provider"

// defaultModels is the built-in catalog of Anthropic (Claude) models ModelMesh
// exposes. As with the OpenAI adapter, it is code-defined, static (no network),
// and overridable via Config.Models.
//
// Anthropic does not offer an embeddings API in this SDK, so every model here is
// chat-only; Embeddings() returns a well-defined unsupported error.
func defaultModels() []provider.ModelInfo {
	return []provider.ModelInfo{
		{
			ID:            "claude-sonnet-4-5",
			Family:        "claude-sonnet",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 200_000,
		},
		{
			ID:            "claude-opus-4-1",
			Family:        "claude-opus",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 200_000,
		},
		{
			ID:            "claude-haiku-4-5",
			Family:        "claude-haiku",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 200_000,
		},
	}
}

// ModelsFromIDs builds chat-capable ModelInfo entries from plain model IDs, used
// when Config.Models overrides the defaults.
func ModelsFromIDs(ids []string) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, provider.ModelInfo{
			ID:           id,
			Capabilities: []provider.Capability{provider.CapabilityChat},
		})
	}
	return out
}
