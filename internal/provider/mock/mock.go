// Package mock provides a configurable, deterministic implementation of
// provider.LLMProvider for use in unit and (future) integration tests. It never
// performs network I/O.
//
// The mock is designed to double as a fault-injection tool for later phases:
// the Circuit Breaker phase (Phase 4) will use its error- and latency-injection
// options to drive failover demos without touching real providers.
package mock

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Compile-time assertions that Provider satisfies the contracts.
var (
	_ provider.LLMProvider = (*Provider)(nil)
	_ provider.Lifecycle   = (*Provider)(nil)
)

// Provider is a deterministic, in-memory LLMProvider. The zero value is not
// usable; construct with New.
type Provider struct {
	name string

	models []provider.ModelInfo
	health provider.HealthStatus

	// Injected behavior. When set, these take precedence over the default
	// deterministic behavior, enabling tests to simulate specific outcomes.
	chatFn   func(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error)
	embedFn  func(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error)
	forceErr error
	latency  time.Duration

	// now allows tests to make timestamps deterministic. Defaults to time.Now.
	now func() time.Time

	// initErr, when set, is returned by Initialize to simulate a lifecycle
	// failure. Lifecycle call counts are tracked for assertions.
	initErr       error
	initCalls     atomic.Int32
	shutdownCalls atomic.Int32
}

// Option configures a Provider.
type Option func(*Provider)

// WithName sets the provider name. Defaults to "mock".
func WithName(name string) Option {
	return func(p *Provider) { p.name = name }
}

// WithModels sets the models the provider reports.
func WithModels(models ...provider.ModelInfo) Option {
	return func(p *Provider) { p.models = models }
}

// WithHealth sets the HealthStatus returned by HealthCheck.
func WithHealth(h provider.HealthStatus) Option {
	return func(p *Provider) { p.health = h }
}

// WithChatFunc overrides Chat with a custom function, for precise test control.
func WithChatFunc(fn func(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error)) Option {
	return func(p *Provider) { p.chatFn = fn }
}

// WithEmbeddingsFunc overrides Embeddings with a custom function.
func WithEmbeddingsFunc(fn func(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error)) Option {
	return func(p *Provider) { p.embedFn = fn }
}

// WithError makes every operation return err (wrapped as a *provider.ProviderError).
// This is the primary fault-injection knob for reliability testing.
func WithError(err error) Option {
	return func(p *Provider) { p.forceErr = err }
}

// WithLatency makes every operation sleep for d (respecting context
// cancellation) before returning, to simulate provider latency.
func WithLatency(d time.Duration) Option {
	return func(p *Provider) { p.latency = d }
}

// WithClock injects a deterministic time source for reproducible timestamps.
func WithClock(now func() time.Time) Option {
	return func(p *Provider) { p.now = now }
}

// WithInitError makes Initialize return err, to exercise lifecycle failure paths.
func WithInitError(err error) Option {
	return func(p *Provider) { p.initErr = err }
}

// New constructs a mock Provider with sensible, deterministic defaults.
func New(opts ...Option) *Provider {
	p := &Provider{
		name: "mock",
		models: []provider.ModelInfo{
			{
				ID:            "mock-chat",
				Family:        "mock",
				Capabilities:  []provider.Capability{provider.CapabilityChat},
				ContextWindow: 8192,
			},
			{
				ID:           "mock-embed",
				Family:       "mock",
				Capabilities: []provider.Capability{provider.CapabilityEmbeddings},
			},
		},
		health: provider.HealthStatus{State: provider.HealthStateHealthy},
		now:    time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the provider's configured name.
func (p *Provider) Name() string { return p.name }

// Initialize satisfies provider.Lifecycle, recording the call and returning the
// configured init error (nil by default).
func (p *Provider) Initialize(ctx context.Context) error {
	p.initCalls.Add(1)
	return p.initErr
}

// Shutdown satisfies provider.Lifecycle, recording the call.
func (p *Provider) Shutdown(ctx context.Context) error {
	p.shutdownCalls.Add(1)
	return nil
}

// InitializeCalls reports how many times Initialize has been called.
func (p *Provider) InitializeCalls() int { return int(p.initCalls.Load()) }

// ShutdownCalls reports how many times Shutdown has been called.
func (p *Provider) ShutdownCalls() int { return int(p.shutdownCalls.Load()) }

// Chat returns a deterministic completion that echoes the last user message,
// unless overridden by WithChatFunc or short-circuited by WithError.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := p.gate(ctx, "chat"); err != nil {
		return provider.ChatResponse{}, err
	}
	if p.chatFn != nil {
		return p.chatFn(ctx, req)
	}
	if err := req.Validate(); err != nil {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", err)
	}

	prompt := lastUserMessage(req.Messages)
	content := "mock response to: " + prompt
	promptTokens := estimateTokens(prompt)
	completionTokens := estimateTokens(content)

	return provider.ChatResponse{
		ID:       "mock-" + prompt,
		Model:    resolveModel(req.Model, "mock-chat"),
		Provider: p.name,
		Choices: []provider.Choice{
			{
				Index:        0,
				Message:      provider.ChatMessage{Role: provider.RoleAssistant, Content: content},
				FinishReason: provider.FinishReasonStop,
			},
		},
		Usage: provider.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
		Created: p.now(),
	}, nil
}

// Embeddings returns a deterministic vector per input, unless overridden.
func (p *Provider) Embeddings(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	if err := p.gate(ctx, "embeddings"); err != nil {
		return provider.EmbeddingResponse{}, err
	}
	if p.embedFn != nil {
		return p.embedFn(ctx, req)
	}
	if err := req.Validate(); err != nil {
		return provider.EmbeddingResponse{}, provider.NewError(p.name, "embeddings", err)
	}

	data := make([]provider.Embedding, len(req.Input))
	total := 0
	for i, in := range req.Input {
		data[i] = provider.Embedding{Index: i, Vector: deterministicVector(in)}
		total += estimateTokens(in)
	}

	return provider.EmbeddingResponse{
		Model:    resolveModel(req.Model, "mock-embed"),
		Provider: p.name,
		Data:     data,
		Usage:    provider.Usage{PromptTokens: total, TotalTokens: total},
	}, nil
}

// Models returns the configured model list.
func (p *Provider) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	if err := p.gate(ctx, "models"); err != nil {
		return nil, err
	}
	// Return a copy so callers cannot mutate internal state.
	out := make([]provider.ModelInfo, len(p.models))
	copy(out, p.models)
	return out, nil
}

// HealthCheck returns the configured health status, stamped with the current
// time. A forced error is reported as a check failure.
func (p *Provider) HealthCheck(ctx context.Context) (provider.HealthStatus, error) {
	if err := p.gate(ctx, "health_check"); err != nil {
		return provider.HealthStatus{State: provider.HealthStateUnknown}, err
	}
	h := p.health
	h.Provider = p.name
	h.CheckedAt = p.now()
	return h, nil
}

// gate applies the shared cross-cutting behavior for every operation: context
// cancellation, injected latency, and injected errors.
func (p *Provider) gate(ctx context.Context, op string) error {
	if err := ctx.Err(); err != nil {
		return provider.NewError(p.name, op, provider.ErrTimeout)
	}
	if p.latency > 0 {
		select {
		case <-time.After(p.latency):
		case <-ctx.Done():
			return provider.NewError(p.name, op, provider.ErrTimeout)
		}
	}
	if p.forceErr != nil {
		return provider.NewError(p.name, op, p.forceErr)
	}
	return nil
}

func lastUserMessage(msgs []provider.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleUser {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}

func resolveModel(requested, fallback string) string {
	if requested != "" {
		return requested
	}
	return fallback
}

// estimateTokens is a crude, deterministic token estimate (word count). It is
// good enough for tests and exercising the Usage plumbing; real adapters use the
// provider's reported usage.
func estimateTokens(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return len(strings.Fields(s))
}

// deterministicVector produces a small, stable pseudo-embedding from the input
// so tests can assert exact values without any randomness.
func deterministicVector(s string) []float32 {
	const dims = 4
	v := make([]float32, dims)
	for i, r := range s {
		v[i%dims] += float32(r)
	}
	return v
}
