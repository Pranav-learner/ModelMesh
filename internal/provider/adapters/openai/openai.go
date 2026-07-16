// Package openai implements the ModelMesh LLMProvider contract for OpenAI.
//
// It is a thin translation layer over the official OpenAI Go SDK: it maps
// unified ModelMesh DTOs to SDK types (see mapping.go), executes the call under
// a bounded retry policy, and normalizes both responses and errors back into the
// unified model. No OpenAI SDK type escapes this package.
//
// Responsibilities: Chat, Embeddings, Models (static catalog), HealthCheck (live
// probe). Retrying is handled by the shared retry helper with the SDK's own
// retries disabled, so retry behavior has a single, testable source.
package openai

import (
	"context"
	"errors"
	"net/http"
	"time"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/adaptererr"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// ProviderName is the stable registry key for this provider.
const ProviderName = "openai"

// Compile-time assertions that Provider satisfies the contracts.
var (
	_ provider.LLMProvider = (*Provider)(nil)
	_ provider.Lifecycle   = (*Provider)(nil)
)

// Config configures the OpenAI adapter. Credentials are injected, never
// hardcoded. All fields are optional except APIKey for real usage; tests supply
// BaseURL (and optionally HTTPClient) to target a local server.
type Config struct {
	// Name overrides the provider name. Defaults to ProviderName ("openai").
	Name string
	// APIKey is the OpenAI credential.
	APIKey string
	// BaseURL overrides the API endpoint (e.g. a proxy or a test server).
	BaseURL string
	// Timeout bounds a single request. Defaults to 30s if zero.
	Timeout time.Duration
	// Models overrides the built-in model catalog. Nil uses defaults.
	Models []provider.ModelInfo
	// Retry configures the retry policy for Chat/Embeddings. Zero uses defaults.
	Retry retry.Policy
	// HTTPClient injects a custom HTTP client (e.g. for tests). Nil creates a
	// dedicated client owned by the provider.
	HTTPClient *http.Client
	// StrictModels, when true, rejects a request whose explicit model is not in
	// the provider's catalog with ErrUnsupportedModel, before any dispatch.
	// Default (false) forwards any model and lets the upstream decide.
	StrictModels bool
}

// Provider is the OpenAI implementation of provider.LLMProvider.
type Provider struct {
	name         string
	client       oai.Client
	httpClient   *http.Client
	models       []provider.ModelInfo
	defaultModel string
	retry        retry.Policy
	timeout      time.Duration
	strictModels bool
}

// New constructs an OpenAI provider from cfg.
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

	// The provider owns an HTTP client so it can release idle connections on
	// Shutdown. Per-request deadlines are applied via context, so the client
	// itself carries no timeout.
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	// Build SDK options. Critically, WithMaxRetries(0) disables the SDK's own
	// retry loop so that our retry helper is the single source of retry behavior.
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
		client:       oai.NewClient(opts...),
		httpClient:   httpClient,
		models:       models,
		defaultModel: defaultChatModel(models),
		retry:        cfg.Retry,
		timeout:      timeout,
		strictModels: cfg.StrictModels,
	}
}

// Initialize satisfies provider.Lifecycle. The OpenAI adapter holds no resources
// that require eager setup, so this is a no-op that simply honors the contract.
func (p *Provider) Initialize(ctx context.Context) error { return nil }

// Shutdown satisfies provider.Lifecycle by releasing idle HTTP connections held
// by the provider's client.
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
	if err := p.ensureModelSupported("chat", req.Model, provider.CapabilityChat); err != nil {
		return provider.ChatResponse{}, err
	}

	params := toChatParams(req, p.resolveModel(req.Model, provider.CapabilityChat))

	var out provider.ChatResponse
	err := retry.Do(ctx, p.retry, func(ctx context.Context) error {
		callCtx, cancel := p.withTimeout(ctx)
		defer cancel()

		resp, err := p.client.Chat.Completions.New(callCtx, params)
		if err != nil {
			return p.translate("chat", err)
		}
		out = fromChatCompletion(p.name, resp)
		return nil
	}, adaptererr.Retryable)

	return out, err
}

// Embeddings computes embeddings for the input batch.
func (p *Provider) Embeddings(ctx context.Context, req provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	if err := req.Validate(); err != nil {
		return provider.EmbeddingResponse{}, provider.NewError(p.name, "embeddings", err)
	}
	if err := p.ensureModelSupported("embeddings", req.Model, provider.CapabilityEmbeddings); err != nil {
		return provider.EmbeddingResponse{}, err
	}

	params := toEmbeddingParams(req, p.resolveModel(req.Model, provider.CapabilityEmbeddings))

	var out provider.EmbeddingResponse
	err := retry.Do(ctx, p.retry, func(ctx context.Context) error {
		callCtx, cancel := p.withTimeout(ctx)
		defer cancel()

		resp, err := p.client.Embeddings.New(callCtx, params)
		if err != nil {
			return p.translate("embeddings", err)
		}
		out = fromEmbeddingResponse(p.name, resp)
		return nil
	}, adaptererr.Retryable)

	return out, err
}

// Models returns a copy of the static, configured model catalog. It performs no
// network I/O.
func (p *Provider) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	out := make([]provider.ModelInfo, len(p.models))
	copy(out, p.models)
	return out, nil
}

// HealthCheck performs a lightweight live probe (a models list) to verify
// connectivity and credentials, and reports a normalized HealthStatus. It does
// not retry: routing wants a single, honest point-in-time reading.
func (p *Provider) HealthCheck(ctx context.Context) (provider.HealthStatus, error) {
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	start := time.Now()
	_, err := p.client.Models.List(callCtx)
	latency := time.Since(start)

	if err != nil {
		translated := p.translate("health_check", err)
		// If the caller's context ended, report that as an error the caller can
		// act on. Otherwise the provider answered (or was unreachable) and we
		// return a definitive unhealthy reading with a nil error.
		if ctx.Err() != nil {
			return provider.HealthStatus{
				Provider:  p.name,
				State:     provider.HealthStateUnhealthy,
				Detail:    translated.Error(),
				CheckedAt: time.Now().UTC(),
				Latency:   latency,
			}, translated
		}
		return provider.HealthStatus{
			Provider:  p.name,
			State:     provider.HealthStateUnhealthy,
			Detail:    translated.Error(),
			CheckedAt: time.Now().UTC(),
			Latency:   latency,
		}, nil
	}

	return provider.HealthStatus{
		Provider:  p.name,
		State:     provider.HealthStateHealthy,
		CheckedAt: time.Now().UTC(),
		Latency:   latency,
	}, nil
}

// translate converts an SDK error into a normalized ModelMesh error. It handles
// context errors, structured API errors (via status code), and unknown errors.
func (p *Provider) translate(op string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := adaptererr.FromContext(p.name, op, err); ctxErr != nil {
		return ctxErr
	}
	var apiErr *oai.Error
	if errors.As(err, &apiErr) {
		return adaptererr.FromStatus(p.name, op, apiErr.StatusCode, apiErr.Message)
	}
	return adaptererr.Unexpected(p.name, op, err)
}

// ensureModelSupported enforces optional strict model validation: when enabled
// and an explicit model is requested, it must exist in the catalog with the
// required capability, else ErrUnsupportedModel is returned before dispatch.
func (p *Provider) ensureModelSupported(op, model string, capability provider.Capability) error {
	if !p.strictModels || model == "" {
		return nil
	}
	if !provider.ModelSupported(p.models, model, capability) {
		return provider.NewErrorf(p.name, op, provider.ErrUnsupportedModel, "model %q is not supported", model)
	}
	return nil
}

// resolveModel returns the requested model, or the provider's default model for
// the given capability when the request leaves it empty.
func (p *Provider) resolveModel(requested string, capability provider.Capability) string {
	if requested != "" {
		return requested
	}
	if capability == provider.CapabilityEmbeddings {
		if m := defaultModelFor(p.models, provider.CapabilityEmbeddings); m != "" {
			return m
		}
	}
	return p.defaultModel
}

// withTimeout derives a per-call context bounded by the provider timeout,
// composing with any deadline already on ctx.
func (p *Provider) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, p.timeout)
}

// defaultChatModel picks the first chat-capable model as the provider default.
func defaultChatModel(models []provider.ModelInfo) string {
	if m := defaultModelFor(models, provider.CapabilityChat); m != "" {
		return m
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}

// defaultModelFor returns the first model advertising the given capability.
func defaultModelFor(models []provider.ModelInfo, capability provider.Capability) string {
	for _, m := range models {
		if m.Supports(capability) {
			return m.ID
		}
	}
	return ""
}
