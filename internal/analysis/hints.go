package analysis

import "fmt"

// HintConfig configures the routing-hint generator: the tier recommended for each
// complexity band, an optional provider recommendation for reasoning-intensive
// requests, and the reasoning-indicator threshold that marks a request
// reasoning-intensive.
type HintConfig struct {
	TierSimple  ModelTier `json:"tier_simple"`
	TierMedium  ModelTier `json:"tier_medium"`
	TierComplex ModelTier `json:"tier_complex"`
	// ReasoningProvider, when set, is recommended for reasoning-intensive requests.
	ReasoningProvider string `json:"reasoning_provider,omitempty"`
	// ReasoningThreshold is the reasoning-indicator count at or above which a
	// request is flagged reasoning-intensive (independent of complexity).
	ReasoningThreshold int `json:"reasoning_threshold"`
}

// DefaultHintConfig returns the default tier mapping and reasoning threshold.
func DefaultHintConfig() HintConfig {
	return HintConfig{
		TierSimple:         TierSmall,
		TierMedium:         TierStandard,
		TierComplex:        TierLarge,
		ReasoningThreshold: 2,
	}
}

func (c HintConfig) withDefaults() HintConfig {
	if c.TierSimple == "" {
		c.TierSimple = TierSmall
	}
	if c.TierMedium == "" {
		c.TierMedium = TierStandard
	}
	if c.TierComplex == "" {
		c.TierComplex = TierLarge
	}
	if c.ReasoningThreshold <= 0 {
		c.ReasoningThreshold = 2
	}
	return c
}

// HintGenerator turns a classification + signals into routing hints. It is an
// interface so the mapping can be replaced (e.g. per-tenant policies) without
// touching the engine.
type HintGenerator interface {
	// Generate enriches h with tier and sensitivity hints and returns the ordered
	// human-readable reasons for each hint.
	Generate(s Signals, c Classification, h *RoutingHints) []string
}

// RuleHintGenerator is the default deterministic hint generator.
type RuleHintGenerator struct {
	cfg HintConfig
}

// Compile-time assertion.
var _ HintGenerator = (*RuleHintGenerator)(nil)

// NewHintGenerator constructs a hint generator, applying config defaults.
func NewHintGenerator(cfg HintConfig) *RuleHintGenerator {
	return &RuleHintGenerator{cfg: cfg.withDefaults()}
}

// Generate maps complexity → tier and derives cost/latency/context/reasoning
// sensitivities from the complexity and signals.
func (g *RuleHintGenerator) Generate(s Signals, c Classification, h *RoutingHints) []string {
	var reasons []string
	h.Complexity = c.Complexity

	// Model tier from complexity.
	switch c.Complexity {
	case ComplexityComplex:
		h.PreferredModelTier = g.cfg.TierComplex
	case ComplexityMedium:
		h.PreferredModelTier = g.cfg.TierMedium
	default:
		h.PreferredModelTier = g.cfg.TierSimple
	}
	reasons = append(reasons, fmt.Sprintf("%s complexity → %s tier", c.Complexity, h.PreferredModelTier))

	// Simple prompts are latency- and cost-sensitive; medium prompts are
	// cost-sensitive. Complex prompts prioritize capability over both.
	switch c.Complexity {
	case ComplexitySimple:
		h.LatencySensitive = true
		h.CostSensitive = true
		reasons = append(reasons, "simple prompt → latency- and cost-sensitive")
	case ComplexityMedium:
		h.CostSensitive = true
		reasons = append(reasons, "medium prompt → cost-sensitive")
	}

	// High context favors large-context models.
	if s.LongContext {
		h.HighContext = true
		reasons = append(reasons, "large input context → high-context")
	}

	// Reasoning intensity: complex prompts, math, or enough reasoning cues.
	if c.Complexity == ComplexityComplex || s.HasMath || s.ReasoningIndicators >= g.cfg.ReasoningThreshold {
		h.ReasoningIntensive = true
		reasons = append(reasons, "reasoning cues / math / complex → reasoning-intensive")
		if g.cfg.ReasoningProvider != "" {
			h.PreferredProvider = g.cfg.ReasoningProvider
			reasons = append(reasons, fmt.Sprintf("reasoning-intensive → prefer provider %q", g.cfg.ReasoningProvider))
		}
	}

	return reasons
}
