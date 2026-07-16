package bootstrap

import (
	"fmt"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/anthropic"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/openai"
	"github.com/symbiotes/modelmesh/internal/provider/factory"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// DefaultFactory returns a provider factory pre-registered with the built-in
// adapters (OpenAI and Anthropic). It is the composition-root wiring point:
// adding a new built-in provider means registering one more builder here, and
// nothing else in the system changes.
//
// Builders live here (in the composition root) rather than inside the adapter
// packages, which keeps adapters free of any dependency on the factory.
func DefaultFactory(deps factory.Deps) *factory.Factory {
	f := factory.New(deps)
	// Errors here would indicate a duplicate built-in registration, i.e. a
	// programming error; panic-free handling keeps New(deps) infallible while
	// still surfacing the mistake loudly if it ever occurs.
	mustRegister(f, openai.ProviderName, openAIBuilder)
	mustRegister(f, anthropic.ProviderName, anthropicBuilder)
	return f
}

func mustRegister(f *factory.Factory, name string, b factory.BuilderFunc) {
	if err := f.Register(name, b); err != nil {
		panic(fmt.Sprintf("bootstrap: default factory registration failed: %v", err))
	}
}

// openAIBuilder constructs an OpenAI provider, enforcing the provider-specific
// requirement that an API key is present.
func openAIBuilder(pc config.ProviderConfig, deps factory.Deps) (provider.LLMProvider, error) {
	if pc.APIKey == "" {
		return nil, fmt.Errorf("%w: provider %q requires an API key", config.ErrInvalidConfig, pc.Name)
	}
	return openai.New(openai.Config{
		Name:    pc.Name,
		APIKey:  pc.APIKey,
		BaseURL: pc.BaseURL,
		Timeout: pc.ResolvedTimeout(deps.DefaultTimeout),
		Models:  optionalModels(pc.Models, openai.ModelsFromIDs),
		Retry:   retry.Policy{MaxRetries: deps.RetryCount},
	}), nil
}

// anthropicBuilder constructs an Anthropic provider, enforcing the API-key
// requirement.
func anthropicBuilder(pc config.ProviderConfig, deps factory.Deps) (provider.LLMProvider, error) {
	if pc.APIKey == "" {
		return nil, fmt.Errorf("%w: provider %q requires an API key", config.ErrInvalidConfig, pc.Name)
	}
	return anthropic.New(anthropic.Config{
		Name:    pc.Name,
		APIKey:  pc.APIKey,
		BaseURL: pc.BaseURL,
		Timeout: pc.ResolvedTimeout(deps.DefaultTimeout),
		Models:  optionalModels(pc.Models, anthropic.ModelsFromIDs),
		Retry:   retry.Policy{MaxRetries: deps.RetryCount},
	}), nil
}

// optionalModels applies a conversion only when model IDs are configured,
// otherwise returning nil so the adapter uses its built-in catalog.
func optionalModels(ids []string, convert func([]string) []provider.ModelInfo) []provider.ModelInfo {
	if len(ids) == 0 {
		return nil
	}
	return convert(ids)
}

// ProvidersFromConfig constructs the enabled providers described by cfg using the
// default factory. It is retained as a convenience over DefaultFactory().BuildAll
// for callers that do not need to customize the factory.
func ProvidersFromConfig(cfg config.Config) ([]provider.LLMProvider, error) {
	cfg = cfg.WithDefaults()
	f := DefaultFactory(factory.Deps{
		DefaultTimeout: cfg.RequestTimeout,
		RetryCount:     cfg.RetryCount,
	})
	return f.BuildAll(cfg)
}
