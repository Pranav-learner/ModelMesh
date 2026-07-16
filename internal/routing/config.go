package routing

import (
	"fmt"
	"time"
)

// Strategy name constants. Only StrategyWeighted is implemented; the remainder
// are reserved extension points that Build reports as not-implemented.
const (
	StrategyWeighted     = "weighted"
	StrategyRoundRobin   = "round_robin"   // reserved
	StrategyRandom       = "random"        // reserved
	StrategyCostFirst    = "cost_first"    // reserved
	StrategyLatencyFirst = "latency_first" // reserved
)

var reservedStrategies = map[string]struct{}{
	StrategyRoundRobin:   {},
	StrategyRandom:       {},
	StrategyCostFirst:    {},
	StrategyLatencyFirst: {},
}

func isReserved(name string) bool {
	_, ok := reservedStrategies[name]
	return ok
}

// Default constants. Naming every default keeps scoring logic free of magic
// numbers: the numbers live here, in configuration defaults, not in algorithms.
const (
	// DefaultWeight is the per-provider tie-break weight for unlisted providers.
	DefaultWeight = 1.0

	// DefaultFactorWeight is the weight given to each scoring factor when none is
	// configured (equal weighting across cost, latency, availability, quality).
	DefaultFactorWeight = 0.25

	// DefaultQualityScore is the quality assigned to a model without a configured
	// quality value.
	DefaultQualityScore = 0.7

	// Availability scores per normalized health state.
	DefaultAvailabilityHealthy   = 1.0
	DefaultAvailabilityDegraded  = 0.5
	DefaultAvailabilityUnhealthy = 0.0
	// DefaultAvailabilityUnknown is used when a provider's health is not known —
	// optimistic-but-not-perfect, so unknown providers remain routable.
	DefaultAvailabilityUnknown = 0.8

	// Default token estimates used by the cost scorer when a RoutingContext does
	// not supply them via Attributes.
	DefaultEstimatedInputTokens  = 1000
	DefaultEstimatedOutputTokens = 300
)

// DefaultExpectedLatency is the assumed latency for a provider/model without a
// configured expectation.
var DefaultExpectedLatency = 800 * time.Millisecond

// Attribute keys the router recognizes on RoutingContext.Attributes. A future
// classifier phase can populate these; until then, cost estimation uses the
// configured defaults.
const (
	AttrEstimatedInputTokens  = "estimated_input_tokens"
	AttrEstimatedOutputTokens = "estimated_output_tokens"
	// AttrFactorWeights carries a per-request factor-weight override
	// (map[string]float64 keyed by scorer name) used for adaptive, request-aware
	// routing. When present on RoutingContext.Attributes, the weighted strategy
	// scores with these weights instead of its static ones. Absent → static
	// behavior, unchanged.
	AttrFactorWeights = "factor_weights"
)

// FactorWeights are the weights applied to each scoring factor when combining
// them into a final score. They are normalized (to sum to 1) at scoring time, so
// only their relative magnitudes matter.
type FactorWeights struct {
	Cost         float64 `json:"cost"`
	Latency      float64 `json:"latency"`
	Availability float64 `json:"availability"`
	Quality      float64 `json:"quality"`
}

func (f FactorWeights) total() float64 { return f.Cost + f.Latency + f.Availability + f.Quality }

// ToMap projects the factor weights onto the scorer-name-keyed map the weighted
// strategy scores with. It is the bridge the adaptive layer uses to publish
// per-request weights via AttrFactorWeights.
func (f FactorWeights) ToMap() map[string]float64 {
	return map[string]float64{
		ScorerCost:         f.Cost,
		ScorerLatency:      f.Latency,
		ScorerAvailability: f.Availability,
		ScorerQuality:      f.Quality,
	}
}

// ModelPricing is the per-1K-token price of a model, used to estimate cost.
type ModelPricing struct {
	InputPer1K  float64 `json:"input_per_1k"`
	OutputPer1K float64 `json:"output_per_1k"`
	Currency    string  `json:"currency,omitempty"`
}

// CostConfig configures the cost scorer: per-model pricing, a fallback price, and
// the default token estimates used when the request does not carry its own.
type CostConfig struct {
	// Pricing maps model ID to its pricing.
	Pricing map[string]ModelPricing `json:"pricing,omitempty"`
	// Default is the pricing used for models absent from Pricing.
	Default ModelPricing `json:"default"`
	// EstimatedInputTokens / EstimatedOutputTokens are the assumed request sizes.
	EstimatedInputTokens  int `json:"estimated_input_tokens"`
	EstimatedOutputTokens int `json:"estimated_output_tokens"`
}

func (c CostConfig) withDefaults() CostConfig {
	if c.EstimatedInputTokens <= 0 {
		c.EstimatedInputTokens = DefaultEstimatedInputTokens
	}
	if c.EstimatedOutputTokens <= 0 {
		c.EstimatedOutputTokens = DefaultEstimatedOutputTokens
	}
	return c
}

// LatencyConfig configures the latency scorer with expected (configured, not
// live) latencies. Model overrides take precedence over provider values.
type LatencyConfig struct {
	Providers map[string]time.Duration `json:"providers,omitempty"`
	Models    map[string]time.Duration `json:"models,omitempty"`
	Default   time.Duration            `json:"default"`
}

func (c LatencyConfig) withDefaults() LatencyConfig {
	if c.Default <= 0 {
		c.Default = DefaultExpectedLatency
	}
	return c
}

// QualityConfig configures the quality scorer with configured quality values in
// [0,1]. Model values take precedence over provider values.
type QualityConfig struct {
	Models    map[string]float64 `json:"models,omitempty"`
	Providers map[string]float64 `json:"providers,omitempty"`
	Default   float64            `json:"default"`
}

func (c QualityConfig) withDefaults() QualityConfig {
	if c.Default == 0 {
		c.Default = DefaultQualityScore
	}
	return c
}

// AvailabilityConfig maps normalized health states to availability scores, with a
// score for unknown health and optional static per-provider overrides.
type AvailabilityConfig struct {
	Healthy   float64            `json:"healthy"`
	Degraded  float64            `json:"degraded"`
	Unhealthy float64            `json:"unhealthy"`
	Unknown   float64            `json:"unknown"`
	Overrides map[string]float64 `json:"overrides,omitempty"`
}

// isZero reports whether the config is the zero value (so defaults may apply).
func (c AvailabilityConfig) isZero() bool {
	return c.Healthy == 0 && c.Degraded == 0 && c.Unhealthy == 0 && c.Unknown == 0 && len(c.Overrides) == 0
}

func (c AvailabilityConfig) withDefaults() AvailabilityConfig {
	if c.isZero() {
		return AvailabilityConfig{
			Healthy:   DefaultAvailabilityHealthy,
			Degraded:  DefaultAvailabilityDegraded,
			Unhealthy: DefaultAvailabilityUnhealthy,
			Unknown:   DefaultAvailabilityUnknown,
		}
	}
	return c
}

// WeightedConfig configures the weighted strategy.
//
// Fields from Phase 2 Part 1 (Weights, DefaultWeight) now serve tie-breaking:
// per-provider Weights act as a priority when two candidates score equally. The
// remaining fields (Part 2) configure the scoring pipeline.
type WeightedConfig struct {
	// Weights is a per-provider priority used for deterministic tie-breaking.
	Weights map[string]float64 `json:"weights,omitempty"`
	// DefaultWeight is the tie-break weight for providers absent from Weights.
	DefaultWeight float64 `json:"default_weight"`

	// Factors are the scoring-factor weights.
	Factors FactorWeights `json:"factors"`
	// Cost, Latency, Quality, Availability configure the individual scorers.
	Cost         CostConfig         `json:"cost"`
	Latency      LatencyConfig      `json:"latency"`
	Quality      QualityConfig      `json:"quality"`
	Availability AvailabilityConfig `json:"availability"`

	// TieBreak is an explicit provider priority order applied before Weights when
	// breaking score ties (earlier = higher priority).
	TieBreak []string `json:"tie_break,omitempty"`
}

func (c WeightedConfig) withDefaults() WeightedConfig {
	if c.DefaultWeight == 0 {
		c.DefaultWeight = DefaultWeight
	}
	if c.Factors.total() == 0 {
		c.Factors = FactorWeights{
			Cost:         DefaultFactorWeight,
			Latency:      DefaultFactorWeight,
			Availability: DefaultFactorWeight,
			Quality:      DefaultFactorWeight,
		}
	}
	c.Cost = c.Cost.withDefaults()
	c.Latency = c.Latency.withDefaults()
	c.Quality = c.Quality.withDefaults()
	c.Availability = c.Availability.withDefaults()
	return c
}

// Config configures the routing framework.
type Config struct {
	// Strategy is the active strategy name. Defaults to StrategyWeighted.
	Strategy string `json:"strategy"`
	// AllowFallback is the default fallback intent for routing contexts.
	AllowFallback bool `json:"allow_fallback"`
	// Weighted holds the weighted strategy configuration.
	Weighted WeightedConfig `json:"weighted"`
}

// DefaultConfig returns a valid default routing configuration with equal factor
// weights and sensible scorer defaults.
func DefaultConfig() Config {
	return Config{
		Strategy:      StrategyWeighted,
		AllowFallback: true,
		Weighted:      WeightedConfig{}.withDefaults(),
	}
}

// WithDefaults returns a copy of c with zero-valued fields filled in.
func (c Config) WithDefaults() Config {
	if c.Strategy == "" {
		c.Strategy = StrategyWeighted
	}
	c.Weighted = c.Weighted.withDefaults()
	return c
}

// Validate checks the routing configuration for structural validity. Errors wrap
// ErrInvalidRoutingConfig.
func (c Config) Validate() error {
	if c.Strategy == "" {
		return fmt.Errorf("%w: strategy must not be empty", ErrInvalidRoutingConfig)
	}
	return c.Weighted.Validate()
}

// Validate checks the weighted configuration: non-negative weights and pricing,
// quality and availability within [0,1], and a positive total factor weight.
func (c WeightedConfig) Validate() error {
	if c.DefaultWeight < 0 {
		return fmt.Errorf("%w: weighted.default_weight must not be negative", ErrInvalidRoutingConfig)
	}
	for name, w := range c.Weights {
		if w < 0 {
			return fmt.Errorf("%w: weighted.weights[%q] must not be negative", ErrInvalidRoutingConfig, name)
		}
	}
	f := c.Factors
	for name, w := range map[string]float64{"cost": f.Cost, "latency": f.Latency, "availability": f.Availability, "quality": f.Quality} {
		if w < 0 {
			return fmt.Errorf("%w: weighted.factors.%s must not be negative", ErrInvalidRoutingConfig, name)
		}
	}
	if f.total() <= 0 {
		return fmt.Errorf("%w: total factor weight must be positive", ErrInvalidRoutingConfig)
	}
	if err := validatePricing(c.Cost); err != nil {
		return err
	}
	if err := validateUnitInterval("quality", c.Quality.Default, c.Quality.Models, c.Quality.Providers); err != nil {
		return err
	}
	if err := validateAvailability(c.Availability); err != nil {
		return err
	}
	if err := validateLatency(c.Latency); err != nil {
		return err
	}
	return nil
}

func validatePricing(c CostConfig) error {
	check := func(p ModelPricing, label string) error {
		if p.InputPer1K < 0 || p.OutputPer1K < 0 {
			return fmt.Errorf("%w: cost pricing %s must not be negative", ErrInvalidRoutingConfig, label)
		}
		return nil
	}
	if err := check(c.Default, "default"); err != nil {
		return err
	}
	for model, p := range c.Pricing {
		if err := check(p, model); err != nil {
			return err
		}
	}
	if c.EstimatedInputTokens < 0 || c.EstimatedOutputTokens < 0 {
		return fmt.Errorf("%w: cost token estimates must not be negative", ErrInvalidRoutingConfig)
	}
	return nil
}

func validateUnitInterval(kind string, def float64, maps ...map[string]float64) error {
	if def < 0 || def > 1 {
		return fmt.Errorf("%w: %s default %v must be within [0,1]", ErrInvalidRoutingConfig, kind, def)
	}
	for _, m := range maps {
		for key, v := range m {
			if v < 0 || v > 1 {
				return fmt.Errorf("%w: %s[%q] %v must be within [0,1]", ErrInvalidRoutingConfig, kind, key, v)
			}
		}
	}
	return nil
}

func validateAvailability(c AvailabilityConfig) error {
	for label, v := range map[string]float64{"healthy": c.Healthy, "degraded": c.Degraded, "unhealthy": c.Unhealthy, "unknown": c.Unknown} {
		if v < 0 || v > 1 {
			return fmt.Errorf("%w: availability.%s %v must be within [0,1]", ErrInvalidRoutingConfig, label, v)
		}
	}
	return validateUnitInterval("availability.overrides", 0, c.Overrides)
}

func validateLatency(c LatencyConfig) error {
	if c.Default < 0 {
		return fmt.Errorf("%w: latency.default must not be negative", ErrInvalidRoutingConfig)
	}
	for _, m := range []map[string]time.Duration{c.Providers, c.Models} {
		for key, d := range m {
			if d < 0 {
				return fmt.Errorf("%w: latency[%q] must not be negative", ErrInvalidRoutingConfig, key)
			}
		}
	}
	return nil
}
