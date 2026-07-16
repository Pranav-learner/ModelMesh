package provider

import (
	"fmt"
	"time"
)

// This file defines the provider-independent Data Transfer Objects (DTOs) that
// form ModelMesh's unified vocabulary. These types MUST NOT expose any
// provider-specific field. Concrete adapters map their native payloads into and
// out of these structures so that every other component in the system depends
// only on this single shape.

// Role identifies the author of a chat message. It is a string-typed enum so
// that it is self-describing on the wire and in logs while remaining a distinct
// type the compiler can check.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Valid reports whether r is a recognized role.
func (r Role) Valid() bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant:
		return true
	default:
		return false
	}
}

// FinishReason describes why a model stopped generating. The set is normalized
// across providers; adapters map native reasons onto these values.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonContentFilter FinishReason = "content_filter"
	FinishReasonError         FinishReason = "error"
)

// Capability enumerates a discrete capability a model may support.
type Capability string

const (
	CapabilityChat       Capability = "chat"
	CapabilityEmbeddings Capability = "embeddings"
)

// ChatMessage is a single turn in a conversation.
type ChatMessage struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is a provider-independent chat completion request.
//
// Optional sampling parameters are represented as pointers so that "unset" is
// distinguishable from a deliberate zero value (e.g. Temperature == 0). This
// lets an adapter forward only the parameters the caller actually specified and
// otherwise defer to provider defaults.
type ChatRequest struct {
	// Model is a ModelMesh model alias. An empty value means "unspecified" —
	// in later phases the Routing Engine will choose. The Provider Layer treats
	// empty as "use the provider's configured default model".
	Model string `json:"model,omitempty"`

	Messages []ChatMessage `json:"messages"`

	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`

	// Metadata carries opaque, non-semantic labels that are echoed to logs and
	// (in later phases) metrics. It never influences provider behavior.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate performs provider-independent structural validation. It returns an
// error wrapping ErrInvalidRequest so callers can match with errors.Is.
func (r ChatRequest) Validate() error {
	if len(r.Messages) == 0 {
		return fmt.Errorf("%w: at least one message is required", ErrInvalidRequest)
	}
	for i, m := range r.Messages {
		if !m.Role.Valid() {
			return fmt.Errorf("%w: messages[%d].role %q is not a valid role", ErrInvalidRequest, i, m.Role)
		}
		if m.Content == "" {
			return fmt.Errorf("%w: messages[%d].content must not be empty", ErrInvalidRequest, i)
		}
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("%w: max_tokens must not be negative", ErrInvalidRequest)
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return fmt.Errorf("%w: temperature must be within [0, 2]", ErrInvalidRequest)
	}
	if r.TopP != nil && (*r.TopP < 0 || *r.TopP > 1) {
		return fmt.Errorf("%w: top_p must be within [0, 1]", ErrInvalidRequest)
	}
	return nil
}

// Choice is one generated completion alternative.
type Choice struct {
	Index        int          `json:"index"`
	Message      ChatMessage  `json:"message"`
	FinishReason FinishReason `json:"finish_reason"`
}

// Usage reports token consumption for a request. Adapters populate it from the
// provider's usage accounting; it is the basis for future cost computation.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is a provider-independent chat completion response.
type ChatResponse struct {
	ID string `json:"id"`
	// Model is the concrete model that served the request.
	Model string `json:"model"`
	// Provider is the name of the provider that served the request.
	Provider string    `json:"provider"`
	Choices  []Choice  `json:"choices"`
	Usage    Usage     `json:"usage"`
	Created  time.Time `json:"created"`
}

// EmbeddingRequest is a provider-independent embeddings request. Input is always
// a batch; a single string is expressed as a one-element slice so adapters have
// one code path.
type EmbeddingRequest struct {
	Model    string            `json:"model,omitempty"`
	Input    []string          `json:"input"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Validate performs provider-independent structural validation.
func (r EmbeddingRequest) Validate() error {
	if len(r.Input) == 0 {
		return fmt.Errorf("%w: at least one input is required", ErrInvalidRequest)
	}
	for i, in := range r.Input {
		if in == "" {
			return fmt.Errorf("%w: input[%d] must not be empty", ErrInvalidRequest, i)
		}
	}
	return nil
}

// Embedding is a single vector result, keyed to its position in the request
// input so ordering is always recoverable.
type Embedding struct {
	Index  int       `json:"index"`
	Vector []float32 `json:"vector"`
}

// EmbeddingResponse is a provider-independent embeddings response.
type EmbeddingResponse struct {
	Model    string      `json:"model"`
	Provider string      `json:"provider"`
	Data     []Embedding `json:"data"`
	Usage    Usage       `json:"usage"`
}

// HealthState is the normalized health of a provider.
type HealthState string

const (
	HealthStateHealthy   HealthState = "healthy"
	HealthStateDegraded  HealthState = "degraded"
	HealthStateUnhealthy HealthState = "unhealthy"
	// HealthStateUnknown is used when health has not been determined, e.g. a
	// health check has not yet run or failed to produce a verdict.
	HealthStateUnknown HealthState = "unknown"
)

// HealthStatus is the result of a provider health check. The future Circuit
// Breaker and Health Monitor will consume this; the Provider Layer only reports
// a point-in-time reading.
type HealthStatus struct {
	// Provider is the name of the provider this reading is for. It is optional
	// (the caller already knows which provider it asked) but populated by real
	// adapters so a HealthStatus is self-describing when passed around.
	Provider  string        `json:"provider,omitempty"`
	State     HealthState   `json:"state"`
	Detail    string        `json:"detail,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
	Latency   time.Duration `json:"latency,omitempty"`
}

// Healthy is a convenience predicate.
func (h HealthStatus) Healthy() bool { return h.State == HealthStateHealthy }

// ModelInfo describes a single model offered by a provider.
type ModelInfo struct {
	ID            string       `json:"id"`
	Family        string       `json:"family,omitempty"`
	Capabilities  []Capability `json:"capabilities"`
	ContextWindow int          `json:"context_window,omitempty"`
}

// Supports reports whether the model advertises capability c.
func (m ModelInfo) Supports(c Capability) bool {
	for _, cap := range m.Capabilities {
		if cap == c {
			return true
		}
	}
	return false
}

// ProviderCapabilities is a coarse, provider-level summary of what a provider
// can do. Streaming is reserved for a future phase and is always false today.
type ProviderCapabilities struct {
	Chat       bool `json:"chat"`
	Embeddings bool `json:"embeddings"`
	Streaming  bool `json:"streaming"`
}

// ProviderInfo is a descriptor of a provider suitable for discovery endpoints
// and dashboards. It is assembled by the Manager (see DescribeProvider) rather
// than being a method on the interface, which keeps the LLMProvider contract
// minimal.
type ProviderInfo struct {
	Name         string               `json:"name"`
	Capabilities ProviderCapabilities `json:"capabilities"`
	Models       []ModelInfo          `json:"models"`
}
