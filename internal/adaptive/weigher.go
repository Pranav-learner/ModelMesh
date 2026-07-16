package adaptive

import (
	"fmt"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// Adjustment records a single factor-weight change for explainability.
type Adjustment struct {
	Factor string  `json:"factor"`
	From   float64 `json:"from"`
	To     float64 `json:"to"`
	Reason string  `json:"reason"`
}

// Result is the outcome of adapting the routing weights for one request: the base
// weights, the adjusted weights, the classified complexity, and the ordered list
// of adjustments that got from one to the other.
type Result struct {
	Complexity  analysis.Complexity   `json:"complexity"`
	Base        routing.FactorWeights `json:"base"`
	Adjusted    routing.FactorWeights `json:"adjusted"`
	Adjustments []Adjustment          `json:"adjustments"`
}

// Changed reports whether any weight was adjusted.
func (r Result) Changed() bool { return len(r.Adjustments) > 0 }

// Config configures the adaptive weighting policy: the base weights and the delta
// applied to each factor for each signal. Every magnitude is tunable, so the
// adjustment strategy is fully configurable.
type Config struct {
	// Base is the starting factor-weight set adaptation adjusts from.
	Base routing.FactorWeights `json:"base"`

	// Complexity deltas.
	SimpleCostBoost      float64 `json:"simple_cost_boost"`
	SimpleQualityPenalty float64 `json:"simple_quality_penalty"`
	ComplexQualityBoost  float64 `json:"complex_quality_boost"`
	ComplexCostPenalty   float64 `json:"complex_cost_penalty"`

	// Hint deltas.
	LatencyBoost            float64 `json:"latency_boost"`
	CostBoost               float64 `json:"cost_boost"`
	HighContextQualityBoost float64 `json:"high_context_quality_boost"`
	ReasoningQualityBoost   float64 `json:"reasoning_quality_boost"`

	// MinWeight is the floor every factor weight is clamped to, so no factor is
	// ever driven to zero or negative.
	MinWeight float64 `json:"min_weight"`

	// ModelTiers maps model IDs to tiers, used to score routing accuracy (chosen
	// model's tier vs the recommended tier). Optional.
	ModelTiers map[string]analysis.ModelTier `json:"model_tiers,omitempty"`
}

// DefaultConfig returns a balanced starting policy: equal base weights with
// moderate, opinionated deltas.
func DefaultConfig() Config {
	return Config{
		Base:                    routing.FactorWeights{Cost: 0.25, Latency: 0.25, Availability: 0.25, Quality: 0.25},
		SimpleCostBoost:         0.20,
		SimpleQualityPenalty:    0.15,
		ComplexQualityBoost:     0.25,
		ComplexCostPenalty:      0.15,
		LatencyBoost:            0.20,
		CostBoost:               0.15,
		HighContextQualityBoost: 0.10,
		ReasoningQualityBoost:   0.15,
		MinWeight:               0.05,
	}
}

func (c Config) withDefaults() Config {
	if weightSum(c.Base) == 0 {
		c.Base = DefaultConfig().Base
	}
	if c.MinWeight <= 0 {
		c.MinWeight = 0.05
	}
	return c
}

// Validate reports whether the configuration is usable.
func (c Config) Validate() error {
	if weightSum(c.Base) <= 0 {
		return fmt.Errorf("adaptive: base weights must sum to a positive value")
	}
	if c.MinWeight < 0 {
		return fmt.Errorf("adaptive: min_weight must not be negative")
	}
	return nil
}

// tierOf returns the configured tier for a model, or "" if unknown.
func (c Config) tierOf(model string) analysis.ModelTier { return c.ModelTiers[model] }

// weightSum returns the sum of a factor-weight set.
func weightSum(f routing.FactorWeights) float64 {
	return f.Cost + f.Latency + f.Availability + f.Quality
}

// Weigher adapts routing weights from analysis hints. It is deterministic and
// side-effect free apart from emitting metrics.
type Weigher struct {
	cfg     Config
	metrics Metrics
	log     logger.Logger
}

// Option configures a Weigher.
type Option func(*Weigher)

// WithMetrics injects a resource-metrics sink. A nil sink is ignored (default Nop).
func WithMetrics(m Metrics) Option {
	return func(w *Weigher) {
		if m != nil {
			w.metrics = m
		}
	}
}

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) Option {
	return func(w *Weigher) {
		if l != nil {
			w.log = l
		}
	}
}

// New constructs a Weigher from configuration, applying defaults.
func New(cfg Config, opts ...Option) *Weigher {
	w := &Weigher{cfg: cfg.withDefaults(), metrics: NopMetrics{}, log: logger.Nop()}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Adapt maps analysis hints to per-request factor weights and returns the full
// adjustment record. It also emits the classification, hint-usage, and
// weight-change metrics.
func (w *Weigher) Adapt(h analysis.RoutingHints) Result {
	base := w.cfg.Base
	fw := base
	var adjustments []Adjustment

	apply := func(factor string, ptr *float64, delta float64, reason string) {
		if delta == 0 {
			return
		}
		from := *ptr
		to := from + delta
		if to < w.cfg.MinWeight {
			to = w.cfg.MinWeight
		}
		if to == from {
			return
		}
		*ptr = to
		adjustments = append(adjustments, Adjustment{Factor: factor, From: from, To: to, Reason: reason})
		w.metrics.WeightChange(factor, from, to)
	}

	switch h.Complexity {
	case analysis.ComplexitySimple:
		apply(routing.ScorerCost, &fw.Cost, w.cfg.SimpleCostBoost, "simple → favor cost")
		apply(routing.ScorerQuality, &fw.Quality, -w.cfg.SimpleQualityPenalty, "simple → de-emphasize quality")
	case analysis.ComplexityComplex:
		apply(routing.ScorerQuality, &fw.Quality, w.cfg.ComplexQualityBoost, "complex → favor quality")
		apply(routing.ScorerCost, &fw.Cost, -w.cfg.ComplexCostPenalty, "complex → de-emphasize cost")
	}
	if h.LatencySensitive {
		apply(routing.ScorerLatency, &fw.Latency, w.cfg.LatencyBoost, "latency-sensitive → favor latency")
		w.metrics.HintUsed(analysis.AttrLatencySensitive)
	}
	if h.CostSensitive {
		apply(routing.ScorerCost, &fw.Cost, w.cfg.CostBoost, "cost-sensitive → favor cost")
		w.metrics.HintUsed(analysis.AttrCostSensitive)
	}
	if h.HighContext {
		apply(routing.ScorerQuality, &fw.Quality, w.cfg.HighContextQualityBoost, "high-context → favor capable models")
		w.metrics.HintUsed(analysis.AttrHighContext)
	}
	if h.ReasoningIntensive {
		apply(routing.ScorerQuality, &fw.Quality, w.cfg.ReasoningQualityBoost, "reasoning-intensive → favor quality")
		w.metrics.HintUsed(analysis.AttrReasoningIntensive)
	}

	w.metrics.Classification(string(h.Complexity))
	w.log.Debug("adapted routing weights",
		logger.String("complexity", string(h.Complexity)),
		logger.Int("adjustments", len(adjustments)),
	)
	return Result{Complexity: h.Complexity, Base: base, Adjusted: fw, Adjustments: adjustments}
}

// RecordOutcome scores routing accuracy for one decision: whether the chosen
// model's tier matches the tier the classification recommended. It is a no-op when
// no tier map is configured or the chosen model's tier is unknown.
func (w *Weigher) RecordOutcome(preferred analysis.ModelTier, chosenModel string) {
	if preferred == "" || len(w.cfg.ModelTiers) == 0 {
		return
	}
	chosen := w.cfg.tierOf(chosenModel)
	if chosen == "" {
		return
	}
	w.metrics.RoutingOutcome(string(preferred), string(chosen), chosen == preferred)
}
