package bootstrap

import (
	"fmt"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/anthropic"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/openai"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// ProvidersFromConfig constructs concrete provider adapters from the provider
// entries in cfg. It is the single place that maps configuration to adapters, so
// adding a new provider type is a one-case change here and nowhere else.
//
// Disabled entries are skipped. An unknown provider name is a configuration
// error and fails fast. Credentials come from the (environment-sourced)
// ProviderConfig.APIKey and are never hardcoded.
//
// The returned providers are ready to hand to New for registration.
func ProvidersFromConfig(cfg config.Config) ([]provider.LLMProvider, error) {
	cfg = cfg.WithDefaults()

	retryPolicy := retry.Policy{MaxRetries: cfg.RetryCount}

	var providers []provider.LLMProvider
	for _, pc := range cfg.Providers {
		if !pc.Enabled {
			continue
		}

		switch pc.Name {
		case openai.ProviderName:
			providers = append(providers, openai.New(openai.Config{
				APIKey:  pc.APIKey,
				BaseURL: pc.BaseURL,
				Timeout: pc.ResolvedTimeout(cfg.RequestTimeout),
				Models:  openaiModels(pc.Models),
				Retry:   retryPolicy,
			}))

		case anthropic.ProviderName:
			providers = append(providers, anthropic.New(anthropic.Config{
				APIKey:  pc.APIKey,
				BaseURL: pc.BaseURL,
				Timeout: pc.ResolvedTimeout(cfg.RequestTimeout),
				Models:  anthropicModels(pc.Models),
				Retry:   retryPolicy,
			}))

		default:
			return nil, fmt.Errorf("bootstrap: unknown provider %q in configuration", pc.Name)
		}
	}
	return providers, nil
}

// openaiModels converts optional configured model IDs into ModelInfo, or returns
// nil so the adapter uses its built-in defaults.
func openaiModels(ids []string) []provider.ModelInfo {
	if len(ids) == 0 {
		return nil
	}
	return openai.ModelsFromIDs(ids)
}

func anthropicModels(ids []string) []provider.ModelInfo {
	if len(ids) == 0 {
		return nil
	}
	return anthropic.ModelsFromIDs(ids)
}
