package budget

import "fmt"

// Policy name constants. Reject and Downgrade are implemented; the rest are
// reserved extension points, nameable in config and reported as not-implemented.
const (
	PolicyReject    = "reject"
	PolicyDowngrade = "downgrade"

	PolicyQueue    = "queue"    // reserved
	PolicyNotify   = "notify"   // reserved
	PolicyApproval = "approval" // reserved
)

var reservedPolicies = map[string]struct{}{
	PolicyQueue:    {},
	PolicyNotify:   {},
	PolicyApproval: {},
}

func isReserved(name string) bool {
	_, ok := reservedPolicies[name]
	return ok
}

// Token-estimate defaults, used when a request does not carry token counts.
const (
	DefaultEstimatedInputTokens = 1000
	DefaultExpectedOutputTokens = 300
	// DefaultDowngradeThreshold is the fraction of the daily limit below which the
	// downgrade policy proactively conserves budget by switching to the default
	// model, even for a request that would otherwise fit.
	DefaultDowngradeThreshold = 0.15
	// DefaultLedgerSize bounds the number of recent usage records retained per
	// budget for introspection.
	DefaultLedgerSize = 256
)

// PricingConfig is the per-model pricing table plus a default for unlisted models.
type PricingConfig struct {
	Models  map[string]ModelPricing `json:"models,omitempty"`
	Default ModelPricing            `json:"default"`
}

// Config configures the budget engine.
type Config struct {
	// Policy is the enforcement policy name (see the Policy* constants).
	Policy string `json:"policy"`
	// DefaultModel is the cheaper model the Downgrade policy switches to.
	DefaultModel string `json:"default_model"`
	// DowngradeThreshold is the fraction of the daily limit [0,1) below which the
	// downgrade policy conserves budget proactively.
	DowngradeThreshold float64 `json:"downgrade_threshold"`
	// DefaultUserDailyLimit / DefaultTeamDailyLimit are the daily limits (USD)
	// applied to budgets looked up but not explicitly registered.
	DefaultUserDailyLimit float64 `json:"default_user_daily_limit"`
	DefaultTeamDailyLimit float64 `json:"default_team_daily_limit"`
	// Pricing is the model pricing table used for cost estimation.
	Pricing PricingConfig `json:"pricing"`
	// EstimatedInputTokens / ExpectedOutputTokens are the token-count defaults used
	// when a request omits them.
	EstimatedInputTokens int `json:"estimated_input_tokens"`
	ExpectedOutputTokens int `json:"expected_output_tokens"`
	// LedgerSize bounds retained usage records per budget.
	LedgerSize int `json:"ledger_size"`
}

// DefaultConfig returns a reject-policy configuration with sensible token and
// ledger defaults. Pricing and daily limits are left to the caller.
func DefaultConfig() Config {
	return Config{
		Policy:               PolicyReject,
		DowngradeThreshold:   DefaultDowngradeThreshold,
		EstimatedInputTokens: DefaultEstimatedInputTokens,
		ExpectedOutputTokens: DefaultExpectedOutputTokens,
		LedgerSize:           DefaultLedgerSize,
	}
}

// WithDefaults returns a copy of c with zero-valued fields replaced by defaults.
func (c Config) WithDefaults() Config {
	if c.Policy == "" {
		c.Policy = PolicyReject
	}
	if c.DowngradeThreshold == 0 {
		c.DowngradeThreshold = DefaultDowngradeThreshold
	}
	if c.EstimatedInputTokens <= 0 {
		c.EstimatedInputTokens = DefaultEstimatedInputTokens
	}
	if c.ExpectedOutputTokens <= 0 {
		c.ExpectedOutputTokens = DefaultExpectedOutputTokens
	}
	if c.LedgerSize <= 0 {
		c.LedgerSize = DefaultLedgerSize
	}
	return c
}

// Validate reports whether the configuration is structurally valid.
func (c Config) Validate() error {
	if c.Policy == "" {
		return fmt.Errorf("%w: policy must not be empty", ErrInvalidConfig)
	}
	if c.DowngradeThreshold < 0 || c.DowngradeThreshold >= 1 {
		return fmt.Errorf("%w: downgrade_threshold must be in [0,1), got %g", ErrInvalidConfig, c.DowngradeThreshold)
	}
	if c.DefaultUserDailyLimit < 0 || c.DefaultTeamDailyLimit < 0 {
		return fmt.Errorf("%w: default daily limits must not be negative", ErrInvalidConfig)
	}
	if c.Policy == PolicyDowngrade && c.DefaultModel == "" {
		return fmt.Errorf("%w: downgrade policy requires a default_model", ErrInvalidConfig)
	}
	if err := validatePricing(c.Pricing.Default); err != nil {
		return err
	}
	for model, p := range c.Pricing.Models {
		if err := validatePricing(p); err != nil {
			return fmt.Errorf("%w (model %q)", err, model)
		}
	}
	return nil
}

func validatePricing(p ModelPricing) error {
	if p.InputPer1K < 0 || p.OutputPer1K < 0 {
		return fmt.Errorf("%w: pricing must not be negative", ErrInvalidConfig)
	}
	return nil
}

// defaultLimitFor returns the configured default daily limit for a scope.
func (c Config) defaultLimitFor(scope Scope) float64 {
	if scope == ScopeTeam {
		return c.DefaultTeamDailyLimit
	}
	return c.DefaultUserDailyLimit
}
