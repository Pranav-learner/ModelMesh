package budget

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func testPricing() PricingConfig {
	return PricingConfig{
		Models: map[string]ModelPricing{
			"gpt-4":       {InputPer1K: 0.03, OutputPer1K: 0.06},
			"gpt-4o-mini": {InputPer1K: 0.0005, OutputPer1K: 0.0015},
		},
		Default: ModelPricing{InputPer1K: 0.001, OutputPer1K: 0.002},
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestPricingModel_Estimate(t *testing.T) {
	cm := NewPricingModel(testPricing(), 1000, 300)

	// gpt-4: 1K input * 0.03 + 0.5K output * 0.06 = 0.03 + 0.03 = 0.06
	if got := cm.Estimate("gpt-4", 1000, 500); !approx(got, 0.06) {
		t.Errorf("estimate gpt-4 = %v, want 0.06", got)
	}
	// Unlisted model uses default pricing: 2K*0.001 + 1K*0.002 = 0.002 + 0.002.
	if got := cm.Estimate("mystery", 2000, 1000); !approx(got, 0.004) {
		t.Errorf("estimate default = %v, want 0.004", got)
	}
	// Zero tokens fall back to the configured defaults (1000 in, 300 out).
	if got := cm.Estimate("gpt-4", 0, 0); !approx(got, 1*0.03+0.3*0.06) {
		t.Errorf("estimate with defaults = %v, want %v", got, 1*0.03+0.3*0.06)
	}
}

func TestPricingModel_Actual(t *testing.T) {
	cm := NewPricingModel(testPricing(), 1000, 300)
	// gpt-4: 2K prompt * 0.03 + 1K completion * 0.06 = 0.06 + 0.06 = 0.12
	usage := provider.Usage{PromptTokens: 2000, CompletionTokens: 1000, TotalTokens: 3000}
	if got := cm.Actual("gpt-4", usage); !approx(got, 0.12) {
		t.Errorf("actual gpt-4 = %v, want 0.12", got)
	}
}

func TestPricingModel_PriceFallback(t *testing.T) {
	cm := NewPricingModel(testPricing(), 1000, 300)
	if cm.Price("gpt-4").InputPer1K != 0.03 {
		t.Errorf("listed price wrong")
	}
	if cm.Price("unknown") != (ModelPricing{InputPer1K: 0.001, OutputPer1K: 0.002}) {
		t.Errorf("unlisted price should fall back to default")
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Errorf("empty text should be 0 tokens")
	}
	if got := EstimateTokens("12345678"); got != 2 { // 8 chars / 4
		t.Errorf("EstimateTokens(8 chars) = %d, want 2", got)
	}
}
