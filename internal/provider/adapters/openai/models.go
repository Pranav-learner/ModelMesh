package openai

import "github.com/symbiotes/modelmesh/internal/provider"

// defaultModels is the built-in catalog of OpenAI models ModelMesh exposes. It
// is intentionally code-defined and easy to extend: adding a model is a one-line
// entry here, and callers may override the set entirely via Config.Models.
//
// The catalog is static (not fetched from the API) so that Models() is
// deterministic and requires no network. HealthCheck performs the live probe.
func defaultModels() []provider.ModelInfo {
	return []provider.ModelInfo{
		{
			ID:            "gpt-4.1",
			Family:        "gpt-4.1",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 1_047_576,
		},
		{
			ID:            "gpt-4o",
			Family:        "gpt-4o",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 128_000,
		},
		{
			ID:            "gpt-4o-mini",
			Family:        "gpt-4o",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			ContextWindow: 128_000,
		},
		{
			ID:            "text-embedding-3-small",
			Family:        "text-embedding-3",
			Capabilities:  []provider.Capability{provider.CapabilityEmbeddings},
			ContextWindow: 8_192,
		},
		{
			ID:            "text-embedding-3-large",
			Family:        "text-embedding-3",
			Capabilities:  []provider.Capability{provider.CapabilityEmbeddings},
			ContextWindow: 8_192,
		},
	}
}

// ModelsFromIDs builds ModelInfo entries from a plain list of model IDs, used
// when Config.Models overrides the defaults. Capabilities are inferred by name
// (embedding models contain "embedding"); unknown models default to chat.
func ModelsFromIDs(ids []string) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(ids))
	for _, id := range ids {
		cap := provider.CapabilityChat
		if containsEmbedding(id) {
			cap = provider.CapabilityEmbeddings
		}
		out = append(out, provider.ModelInfo{ID: id, Capabilities: []provider.Capability{cap}})
	}
	return out
}

func containsEmbedding(s string) bool {
	// small, allocation-free substring check
	const sub = "embedding"
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
