package provider

import "context"

// Lifecycle is an OPTIONAL interface a provider may implement to participate in
// coordinated startup and shutdown. It is detected via a type assertion by the
// bootstrap layer; providers that hold no resources need not implement it, and
// the LLMProvider contract stays minimal.
//
// This is the composition-based extension seam for resource management: a
// provider that opens connections, pools, or files implements Lifecycle to
// acquire them in Initialize and release them in Shutdown, without changing the
// core contract every provider must satisfy.
type Lifecycle interface {
	// Initialize acquires any resources the provider needs. It must be safe to
	// call once, before the provider serves traffic. Implementations should be
	// cheap and must respect ctx.
	Initialize(ctx context.Context) error

	// Shutdown releases resources acquired by the provider. It must be safe to
	// call once, and should not block indefinitely; it must respect ctx.
	Shutdown(ctx context.Context) error
}

// ModelSupported reports whether models contains a model with the given id that
// advertises the given capability. It is the basis for optional pre-dispatch
// model validation in adapters.
func ModelSupported(models []ModelInfo, id string, capability Capability) bool {
	for _, m := range models {
		if m.ID == id && m.Supports(capability) {
			return true
		}
	}
	return false
}
