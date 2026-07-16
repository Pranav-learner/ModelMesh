package routing

import (
	"context"
	"fmt"
	"sort"
)

// WeightedStrategy ranks candidates with a configurable weighted scoring engine.
//
// # Pipeline
//
//	candidates -> [cost, latency, availability, quality] scorers -> aggregator
//	           -> weighted final score -> rank (desc) -> tie-break -> explanation
//
// Each scorer returns normalized [0,1] scores; the aggregator combines them with
// normalized factor weights into a final score per candidate; candidates are
// sorted by final score descending with deterministic tie-breaking. The engine is
// modular: additional scorers can be injected via WithScorer without changing the
// aggregation, ranking, or explanation logic.
type WeightedStrategy struct {
	scorers     []Scorer
	weights     map[string]float64 // raw factor weights (by scorer name)
	normWeights map[string]float64 // normalized (cached)

	providerWeights map[string]float64 // per-provider tie-break priority
	defaultWeight   float64
	tieBreak        []string // explicit provider priority order for ties
}

// weightedOptions accumulates constructor options before the scorers are built.
type weightedOptions struct {
	health HealthProvider
	extra  []scorerWeight
}

type scorerWeight struct {
	scorer Scorer
	weight float64
}

// WeightedOption configures a WeightedStrategy at construction.
type WeightedOption func(*weightedOptions)

// WithHealthProvider injects the health source used by the availability scorer.
// Without it, availability falls back to the configured "unknown" score. This is
// the seam the Health Monitoring phase will use.
func WithHealthProvider(hp HealthProvider) WeightedOption {
	return func(o *weightedOptions) {
		if hp != nil {
			o.health = hp
		}
	}
}

// WithScorer registers an additional scoring factor with its weight. This is how
// future factors are added without modifying existing scoring logic.
func WithScorer(s Scorer, weight float64) WeightedOption {
	return func(o *weightedOptions) {
		if s != nil {
			o.extra = append(o.extra, scorerWeight{scorer: s, weight: weight})
		}
	}
}

// NewWeighted builds a weighted strategy from configuration and options.
func NewWeighted(cfg WeightedConfig, opts ...WeightedOption) *WeightedStrategy {
	cfg = cfg.withDefaults()

	o := weightedOptions{health: NoHealth{}}
	for _, opt := range opts {
		opt(&o)
	}

	scorers := []Scorer{
		NewCostScorer(cfg.Cost),
		NewLatencyScorer(cfg.Latency),
		NewAvailabilityScorer(cfg.Availability, o.health),
		NewQualityScorer(cfg.Quality),
	}
	weights := map[string]float64{
		ScorerCost:         cfg.Factors.Cost,
		ScorerLatency:      cfg.Factors.Latency,
		ScorerAvailability: cfg.Factors.Availability,
		ScorerQuality:      cfg.Factors.Quality,
	}
	for _, sw := range o.extra {
		scorers = append(scorers, sw.scorer)
		weights[sw.scorer.Name()] = sw.weight
	}

	return &WeightedStrategy{
		scorers:         scorers,
		weights:         weights,
		normWeights:     normalizeWeights(weights),
		providerWeights: cfg.Weights,
		defaultWeight:   cfg.DefaultWeight,
		tieBreak:        cfg.TieBreak,
	}
}

// Name returns the strategy identifier.
func (s *WeightedStrategy) Name() string { return StrategyWeighted }

// NormalizedWeights returns a copy of the normalized factor weights, satisfying
// the Explainer interface so the router can surface them in explanations.
func (s *WeightedStrategy) NormalizedWeights() map[string]float64 {
	out := make(map[string]float64, len(s.normWeights))
	for k, v := range s.normWeights {
		out[k] = round4(v)
	}
	return out
}

// Rank scores every candidate, ranks them by final score (descending) with
// deterministic tie-breaking, annotates each with its factor breakdown, score,
// and a human-readable reason, and returns them best-first.
func (s *WeightedStrategy) Rank(ctx context.Context, rc RoutingContext, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	breakdowns, err := aggregate(ctx, rc, candidates, s.scorers, s.normWeights)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(breakdowns, func(i, j int) bool {
		if !almostEqual(breakdowns[i].Final, breakdowns[j].Final) {
			return breakdowns[i].Final > breakdowns[j].Final
		}
		return s.tieBreakLess(breakdowns[i].Candidate, breakdowns[j].Candidate)
	})

	reasons := s.reasons(breakdowns)
	out := make([]Candidate, len(breakdowns))
	for i, b := range breakdowns {
		c := b.Candidate
		c.Score = round4(b.Final)
		c.Weight = s.providerWeight(c.Provider)
		c.Factors = roundedFactors(b.Factors)
		c.Reason = reasons[i]
		out[i] = c
	}
	return out, nil
}

// tieBreakLess reports whether candidate a should rank ahead of b when their
// scores are equal. The order is fully deterministic:
//
//  1. explicit TieBreak provider priority (earlier = higher);
//  2. per-provider Weight (higher = higher priority);
//  3. provider name ascending, then model name ascending.
//
// It never uses randomness.
func (s *WeightedStrategy) tieBreakLess(a, b Candidate) bool {
	ia, oka := indexOf(s.tieBreak, a.Provider)
	ib, okb := indexOf(s.tieBreak, b.Provider)
	switch {
	case oka && okb:
		if ia != ib {
			return ia < ib
		}
	case oka:
		return true
	case okb:
		return false
	}

	if wa, wb := s.providerWeight(a.Provider), s.providerWeight(b.Provider); wa != wb {
		return wa > wb
	}

	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	return a.Model < b.Model
}

func (s *WeightedStrategy) providerWeight(name string) float64 {
	if w, ok := s.providerWeights[name]; ok {
		return w
	}
	return s.defaultWeight
}

// reasons builds a human-readable reason per ranked candidate. The winner's
// reason explains the deciding factor against the runner-up; others state their
// rank and final score.
func (s *WeightedStrategy) reasons(ranked []Breakdown) []string {
	out := make([]string, len(ranked))
	for i, b := range ranked {
		out[i] = fmt.Sprintf("rank %d of %d (final %.3f)", i+1, len(ranked), b.Final)
	}
	switch {
	case len(ranked) == 1:
		out[0] = "only eligible candidate"
	case len(ranked) >= 2:
		out[0] = s.winnerReason(ranked[0], ranked[1])
	}
	return out
}

// winnerReason describes why the winner beat the runner-up: the factor with the
// largest positive weighted contribution difference decided it, and any factor
// where the winner was weaker is noted for nuance. Factor iteration is over
// sorted names, so the reason is deterministic.
func (s *WeightedStrategy) winnerReason(winner, runner Breakdown) string {
	var bestFactor, weakFactor string
	bestDelta, weakDelta := 0.0, 0.0

	for _, name := range sortedKeys(s.normWeights) {
		delta := s.normWeights[name] * (winner.Factors[name] - runner.Factors[name])
		if delta > bestDelta {
			bestDelta, bestFactor = delta, name
		}
		if delta < weakDelta {
			weakDelta, weakFactor = delta, name
		}
	}

	reason := fmt.Sprintf("won: final %.3f vs %.3f", winner.Final, runner.Final)
	if bestFactor != "" {
		reason += fmt.Sprintf("; strongest on %s (%.2f vs %.2f)",
			bestFactor, winner.Factors[bestFactor], runner.Factors[bestFactor])
	}
	if weakFactor != "" {
		reason += fmt.Sprintf(", despite weaker %s (%.2f vs %.2f)",
			weakFactor, winner.Factors[weakFactor], runner.Factors[weakFactor])
	}
	return reason
}

func roundedFactors(factors map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(factors))
	for k, v := range factors {
		out[k] = round4(v)
	}
	return out
}

func indexOf(list []string, value string) (int, bool) {
	for i, v := range list {
		if v == value {
			return i, true
		}
	}
	return 0, false
}
