// Package anthropic implements the ModelMesh LLMProvider contract for Anthropic
// (Claude).
//
// Like the OpenAI adapter, it is a thin translation layer over the official
// Anthropic Go SDK: unified DTOs in, SDK types across the wire, unified DTOs and
// normalized errors out. No Anthropic SDK type escapes this package.
//
// Anthropic does not expose an embeddings API in this SDK. Embeddings therefore
// returns a well-defined ErrNotImplemented while the adapter still fully
// satisfies the LLMProvider interface — a deliberate, documented capability gap
// rather than a missing method.
package anthropic

import (
	"context"
	"errors"
	"net/http"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/adaptererr"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// ProviderName is the stable registry key for this provider.
const ProviderName = "anthropic"

// Compile-time assertions that Provider satisfies the contracts.
var (
	_ provider.LLMProvider = (*Provider)(nil)
	_ provider.Lifecycle   = (*Provider)(nil)
)

// Config configures the Anthropic adapter. Credentials are injected, never
// hardcoded.
type Config struct {
	// Name overrides the provider name. Defaults to ProviderName ("anthropic").
	Name string
	// APIKey is the Anthropic credential.
	APIKey string
	// BaseURL overrides the API endpoint (e.g. a proxy or a test server).
	BaseURL string
	// Timeout bounds a single request. Defaults to 30s if zero.
	Timeout time.Duration
	// Models overrides the built-in model catalog. Nil uses defaults.
	Models []provider.ModelInfo
	// Retry configures the retry policy for Chat. Zero uses defaults.
	Retry retry.Policy
	// HTTPClient injects a custom HTTP client (e.g. for tests). Nil creates a
	// dedicated client owned by the provider.
	HTTPClient *http.Client
	// StrictModels, when true, rejects a request whose explicit model is not in
	// the provider's catalog with ErrUnsupportedModel, before any dispatch.
	StrictModels bool
	// now injects a clock for deterministic response timestamps in tests. Nil
	// uses time.Now.
	now func() time.Time
}

// Provider is the Anthropic implementation of provider.LLMProvider.
type Provider struct {
	name         string
	client       ant.Client
	httpClient   *http.Client
	models       []provider.ModelInfo
	defaultModel string
	retry        retry.Policy
	timeout      time.Duration
	strictModels bool
	now          func() time.Time
}

// New constructs an Anthropic provider from cfg.
func New(cfg Config) *Provider {
	name := cfg.Name
	if name == "" {
		name = ProviderName
	}

	models := cfg.Models
	if len(models) == 0 {
		models = defaultModels()
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	now := cfg.now
	if now == nil {
		now = time.Now
	}

	// The provider owns an HTTP client so it can release idle connections on
	// Shutdown. Per-request deadlines are applied via context.
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	// WithMaxRetries(0) disables the SDK's own retry loop so our retry helper is
	// the single source of retry behavior.
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
		option.WithHTTPClient(httpClient),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	return &Provider{
		name:         name,
		client:       ant.NewClient(opts...),
		httpClient:   httpClient,
		models:       models,
		defaultModel: defaultChatModel(models),
		retry:        cfg.Retry,
		timeout:      timeout,
		strictModels: cfg.StrictModels,
		now:          now,
	}
}

// Initialize satisfies provider.Lifecycle. The Anthropic adapter holds no
// resources that require eager setup, so this is a no-op.
func (p *Provider) Initialize(ctx context.Context) error { return nil }

// Shutdown satisfies provider.Lifecycle by releasing idle HTTP connections.
func (p *Provider) Shutdown(ctx context.Context) error {
	p.httpClient.CloseIdleConnections()
	return nil
}

// Name returns the provider's registry name.
func (p *Provider) Name() string { return p.name }

// Chat performs a chat completion, translating to and from the unified model and
// retrying transient failures.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if err := req.Validate(); err != nil {
		return provider.ChatResponse{}, provider.NewError(p.name, "chat", err)
	}
	if p.strictModels && req.Model != "" && !provider.ModelSupported(p.models, req.Model, provider.CapabilityChat) {
		return provider.ChatResponse{}, provider.NewErrorf(p.name, "chat", provider.ErrUnsupportedModel, "model %q is not supported", req.Model)
	}

	params := toMessageParams(req, p.resolveModel(req.Model))

	var out provider.ChatResponse
	err := retry.Do(ctx, p.retry, func(ctx context.Context) error {
		callCtx, cancel := p.withTimeout(ctx)
		defer cancel()

		msg, err := p.client.Messages.New(callCtx, params)
		if err != nil {
			return p.translate("chat", err)
		}
		out = fromMessage(p.name, msg, p.now().UTC())
		return nil
	}, adaptererr.Retryable)

	return out, err
}

// Embeddings is not supported by Anthropic. It returns a normalized
// ErrNotImplemented while still satisfying the interface.
func (p *Provider) Embeddings(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, provider.NewErrorf(
		p.name, "embeddings", provider.ErrNotImplemented,
		"anthropic does not provide an embeddings API",
	)
}

// Models returns a copy of the static, configured model catalog. No network I/O.
func (p *Provider) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	out := make([]provider.ModelInfo, len(p.models))
	copy(out, p.models)
	return out, nil
}

// HealthCheck performs a lightweight live probe (a models list) and reports a
// normalized HealthStatus. It does not retry.
func (p *Provider) HealthCheck(ctx context.Context) (provider.HealthStatus, error) {
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	start := time.Now()
	_, err := p.client.Models.List(callCtx, ant.ModelListParams{})
	latency := time.Since(start)

	if err != nil {
		translated := p.translate("health_check", err)
		status := provider.HealthStatus{
			Provider:  p.name,
			State:     provider.HealthStateUnhealthy,
			Detail:    translated.Error(),
			CheckedAt: time.Now().UTC(),
			Latency:   latency,
		}
		if ctx.Err() != nil {
			return status, translated
		}
		return status, nil
	}

	return provider.HealthStatus{
		Provider:  p.name,
		State:     provider.HealthStateHealthy,
		CheckedAt: time.Now().UTC(),
		Latency:   latency,
	}, nil
}

// translate converts an SDK error into a normalized ModelMesh error.
func (p *Provider) translate(op string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := adaptererr.FromContext(p.name, op, err); ctxErr != nil {
		return ctxErr
	}
	var apiErr *ant.Error
	if errors.As(err, &apiErr) {
		return adaptererr.FromStatus(p.name, op, apiErr.StatusCode, apiErr.Error())
	}
	return adaptererr.Unexpected(p.name, op, err)
}

// resolveModel returns the requested model, or the provider default when empty.
func (p *Provider) resolveModel(requested string) string {
	if requested != "" {
		return requested
	}
	return p.defaultModel
}

// withTimeout derives a per-call context bounded by the provider timeout.
func (p *Provider) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, p.timeout)
}

// defaultChatModel picks the first chat-capable model as the provider default.
func defaultChatModel(models []provider.ModelInfo) string {
	for _, m := range models {
		if m.Supports(provider.CapabilityChat) {
			return m.ID
		}
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}
