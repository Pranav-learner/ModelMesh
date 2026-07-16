package provider

import "context"

// LLMProvider is the contract every concrete provider adapter implements. It is
// the single seam between ModelMesh and the outside world of LLM APIs.
//
// # Why this shape
//
//   - Small and cohesive: five methods, each a single responsibility. A small
//     interface is easy to implement, easy to mock, and cheap to satisfy — which
//     matters because every future provider must satisfy it exactly.
//   - context.Context first: every method that may perform I/O takes a context
//     so callers control cancellation, deadlines, and (later) trace propagation.
//   - Provider-independent types only: inputs and outputs are the DTOs from
//     types.go. No provider-native type ever crosses this boundary.
//
// # Extensibility without breakage
//
// New behavior is added by *composing* additional small interfaces (e.g. a
// future StreamingProvider or ToolCallingProvider) that an adapter may
// optionally also implement, and which callers detect with a type assertion.
// This keeps LLMProvider stable: existing implementations never break when new,
// optional capabilities are introduced. Coarse capability discovery is exposed
// separately via ProviderInfo (see DescribeProvider) so this contract need not
// grow for description purposes.
type LLMProvider interface {
	// Name returns the stable, unique identifier of the provider (e.g. "openai").
	// It must be constant for the lifetime of the instance and is the key under
	// which the provider is registered.
	Name() string

	// Chat performs a chat completion. Implementations must translate req into
	// their native request, execute it under ctx, and return a normalized
	// ChatResponse. Errors should be returned wrapped as a *ProviderError over an
	// appropriate sentinel (see errors.go).
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)

	// Embeddings computes embeddings for the given input batch. Providers that do
	// not support embeddings must return an error wrapping ErrNotImplemented.
	Embeddings(ctx context.Context, req EmbeddingRequest) (EmbeddingResponse, error)

	// Models lists the models the provider offers. It may perform discovery I/O,
	// so it takes a context and returns an error. It is not a hot-path call.
	Models(ctx context.Context) ([]ModelInfo, error)

	// HealthCheck reports a point-in-time health reading for the provider.
	// Implementations should be cheap and side-effect free where possible. A
	// non-nil error indicates the check itself could not be performed; a
	// HealthStatus with a non-healthy State indicates the provider is reachable
	// but not fully healthy.
	HealthCheck(ctx context.Context) (HealthStatus, error)
}

// DescribeProvider assembles a ProviderInfo descriptor for p by combining its
// Name with a Models lookup and inferring coarse ProviderCapabilities from the
// advertised model capabilities.
//
// It lives as a free function rather than an interface method to keep
// LLMProvider minimal: description is a derived concern, not a core capability.
func DescribeProvider(ctx context.Context, p LLMProvider) (ProviderInfo, error) {
	models, err := p.Models(ctx)
	if err != nil {
		return ProviderInfo{}, NewError(p.Name(), "describe", err)
	}

	var caps ProviderCapabilities
	for _, m := range models {
		if m.Supports(CapabilityChat) {
			caps.Chat = true
		}
		if m.Supports(CapabilityEmbeddings) {
			caps.Embeddings = true
		}
	}

	return ProviderInfo{
		Name:         p.Name(),
		Capabilities: caps,
		Models:       models,
	}, nil
}
