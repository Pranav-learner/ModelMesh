package budget

import "github.com/symbiotes/modelmesh/internal/provider"

// ModelPricing is the per-1K-token price of a model in USD. It mirrors the
// routing engine's pricing shape but is owned here so the budget package stays
// self-contained and independently configurable.
type ModelPricing struct {
	InputPer1K  float64 `json:"input_per_1k"`
	OutputPer1K float64 `json:"output_per_1k"`
}

// CostModel is the cost-computation facade used by the engine and by observability
// reporting. It hides pricing and token estimation behind three operations:
// approximate pre-call Estimate, exact post-call Actual, and raw Price lookup.
type CostModel interface {
	// Estimate approximates the cost of a request before it runs, from the model's
	// pricing and the input/expected-output token counts.
	Estimate(model string, inputTokens, expectedOutputTokens int) float64
	// Actual computes the exact cost of a completed request from reported usage.
	Actual(model string, usage provider.Usage) float64
	// Price returns the pricing used for a model (the default if unlisted).
	Price(model string) ModelPricing
}

// Compile-time assertion.
var _ CostModel = (*PricingModel)(nil)

// PricingModel is the static-pricing CostModel: a table of per-model prices with
// a default for unlisted models, plus token-estimate defaults used when a request
// does not supply them.
type PricingModel struct {
	pricing         map[string]ModelPricing
	fallback        ModelPricing
	defaultInput    int
	defaultExpected int
}

// NewPricingModel builds a CostModel from a pricing config and token defaults. A
// non-positive default token count falls back to the package defaults.
func NewPricingModel(cfg PricingConfig, defaultInput, defaultExpected int) *PricingModel {
	if defaultInput <= 0 {
		defaultInput = DefaultEstimatedInputTokens
	}
	if defaultExpected <= 0 {
		defaultExpected = DefaultExpectedOutputTokens
	}
	m := make(map[string]ModelPricing, len(cfg.Models))
	for k, v := range cfg.Models {
		m[k] = v
	}
	return &PricingModel{
		pricing:         m,
		fallback:        cfg.Default,
		defaultInput:    defaultInput,
		defaultExpected: defaultExpected,
	}
}

// Price returns the pricing for a model, falling back to the default.
func (p *PricingModel) Price(model string) ModelPricing {
	if pr, ok := p.pricing[model]; ok {
		return pr
	}
	return p.fallback
}

// Estimate approximates request cost. Non-positive token counts use the
// configured defaults, so a caller that has not counted tokens still gets a
// sensible estimate.
func (p *PricingModel) Estimate(model string, inputTokens, expectedOutputTokens int) float64 {
	if inputTokens <= 0 {
		inputTokens = p.defaultInput
	}
	if expectedOutputTokens <= 0 {
		expectedOutputTokens = p.defaultExpected
	}
	pr := p.Price(model)
	return perThousand(inputTokens)*pr.InputPer1K + perThousand(expectedOutputTokens)*pr.OutputPer1K
}

// Actual computes exact cost from provider-reported token usage.
func (p *PricingModel) Actual(model string, usage provider.Usage) float64 {
	pr := p.Price(model)
	return perThousand(usage.PromptTokens)*pr.InputPer1K + perThousand(usage.CompletionTokens)*pr.OutputPer1K
}

func perThousand(tokens int) float64 { return float64(tokens) / 1000.0 }

// EstimateTokens is a provider-agnostic heuristic for approximate token counts
// from text length (~4 characters per token). It lets callers that have only the
// raw prompt feed a token estimate without a tokenizer dependency.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + 3) / 4
}
