package budget

import (
	"errors"
	"testing"
)

func policyInput(model string, estimate, remaining, limit float64, cfg Config) PolicyInput {
	return PolicyInput{
		Request:   AuthorizeRequest{Scope: ScopeUser, BudgetID: "u", Model: model, InputTokens: 1000, ExpectedOutputTokens: 1000},
		Status:    BudgetStatus{ID: "u", Scope: ScopeUser, DailyLimit: limit, Remaining: remaining},
		Estimate:  estimate,
		CostModel: NewPricingModel(testPricing(), 1000, 300),
		Config:    cfg,
	}
}

func TestRejectPolicy(t *testing.T) {
	cfg := Config{Policy: PolicyReject}.WithDefaults()
	p := RejectPolicy{}

	if d := p.Decide(policyInput("gpt-4", 0.5, 1.0, 2.0, cfg)); d.Outcome != OutcomeAllow {
		t.Errorf("fitting request = %s, want allow", d.Outcome)
	}
	if d := p.Decide(policyInput("gpt-4", 1.5, 1.0, 2.0, cfg)); d.Outcome != OutcomeReject || d.Allowed() {
		t.Errorf("over-budget request = %s (allowed=%v), want reject", d.Outcome, d.Allowed())
	}
}

func TestDowngradePolicy_OverBudgetDowngrades(t *testing.T) {
	cfg := Config{Policy: PolicyDowngrade, DefaultModel: "gpt-4o-mini", DowngradeThreshold: 0.15}.WithDefaults()
	p := DowngradePolicy{}

	// gpt-4 estimate 0.09 doesn't fit remaining 0.05; gpt-4o-mini (0.5*.0005+... tiny) fits → downgrade.
	d := p.Decide(policyInput("gpt-4", 0.09, 0.05, 1.0, cfg))
	if d.Outcome != OutcomeDowngrade {
		t.Fatalf("over-budget = %s, want downgrade", d.Outcome)
	}
	if d.Model != "gpt-4o-mini" || d.OriginalModel != "gpt-4" {
		t.Errorf("downgrade model = %s (from %s), want gpt-4o-mini", d.Model, d.OriginalModel)
	}
	if !d.Downgraded() || !d.Allowed() {
		t.Errorf("downgrade should be allowed and flagged")
	}
}

func TestDowngradePolicy_ProactiveBelowThreshold(t *testing.T) {
	cfg := Config{Policy: PolicyDowngrade, DefaultModel: "gpt-4o-mini", DowngradeThreshold: 0.15}.WithDefaults()
	p := DowngradePolicy{}

	// Request fits (0.05 <= 0.10) but remaining 0.10 < 0.15*1.0 → proactive downgrade.
	d := p.Decide(policyInput("gpt-4", 0.05, 0.10, 1.0, cfg))
	if d.Outcome != OutcomeDowngrade {
		t.Errorf("below-threshold fitting request = %s, want proactive downgrade", d.Outcome)
	}
}

func TestDowngradePolicy_RejectsWhenNoAffordableDowngrade(t *testing.T) {
	cfg := Config{Policy: PolicyDowngrade, DefaultModel: "gpt-4o-mini", DowngradeThreshold: 0.15}.WithDefaults()
	p := DowngradePolicy{}

	// Remaining 0.0001 too small even for the mini model → reject.
	d := p.Decide(policyInput("gpt-4", 0.09, 0.0001, 1.0, cfg))
	if d.Outcome != OutcomeReject {
		t.Errorf("no affordable downgrade = %s, want reject", d.Outcome)
	}
}

func TestDowngradePolicy_NoCheaperModelButFits(t *testing.T) {
	// DefaultModel equals the requested model: no downgrade target, but the
	// request fits, so it is allowed even below threshold.
	cfg := Config{Policy: PolicyDowngrade, DefaultModel: "mini", DowngradeThreshold: 0.15}.WithDefaults()
	d := DowngradePolicy{}.Decide(policyInput("mini", 0.05, 0.10, 1.0, cfg))
	if d.Outcome != OutcomeAllow {
		t.Errorf("no cheaper model but fits = %s, want allow", d.Outcome)
	}
	if d.Model != "mini" {
		t.Errorf("model changed unexpectedly to %s", d.Model)
	}
}

func TestPolicyRegistry_Build(t *testing.T) {
	reg := DefaultPolicyRegistry()
	if p, err := reg.Build(PolicyReject, DefaultConfig()); err != nil || p.Name() != PolicyReject {
		t.Errorf("build reject = (%v,%v)", p, err)
	}
	if _, err := reg.Build(PolicyApproval, DefaultConfig()); !errors.Is(err, ErrPolicyNotImplemented) {
		t.Errorf("reserved = %v, want ErrPolicyNotImplemented", err)
	}
	if _, err := reg.Build("bogus", DefaultConfig()); !errors.Is(err, ErrUnknownPolicy) {
		t.Errorf("unknown = %v, want ErrUnknownPolicy", err)
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
	bad := []Config{
		{Policy: "", DowngradeThreshold: 0.1},
		{Policy: PolicyReject, DowngradeThreshold: 1.0},
		{Policy: PolicyReject, DowngradeThreshold: -0.1},
		{Policy: PolicyDowngrade, DowngradeThreshold: 0.1}, // missing default_model
		{Policy: PolicyReject, DefaultUserDailyLimit: -1},
		{Policy: PolicyReject, Pricing: PricingConfig{Default: ModelPricing{InputPer1K: -1}}},
	}
	for i, c := range bad {
		if err := c.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("bad config %d validated: %v", i, err)
		}
	}
}
