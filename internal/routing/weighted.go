package routing

import "context"

// WeightedStrategy is the skeleton of the weighted routing strategy.
//
// # Phase 2 Part 1 behavior (no scoring)
//
// This part establishes the strategy's structure and wiring only. Rank attaches
// each candidate's configured weight as metadata and returns candidates in their
// existing (stable) order — it does NOT yet rank by weight. Weighted selection
// (normalizing weights and ordering/probabilistically choosing by them) is
// implemented in Phase 2 Part 2. Keeping the ordering stable here makes the
// "framework, not scoring" boundary explicit and testable.
type WeightedStrategy struct {
	cfg WeightedConfig
}

// NewWeighted constructs a weighted strategy from its configuration, applying a
// sane default weight when unset.
func NewWeighted(cfg WeightedConfig) *WeightedStrategy {
	if cfg.DefaultWeight <= 0 {
		cfg.DefaultWeight = DefaultWeight
	}
	return &WeightedStrategy{cfg: cfg}
}

// Name returns the strategy identifier.
func (s *WeightedStrategy) Name() string { return StrategyWeighted }

// Rank attaches configured weights and returns the candidates in stable order.
//
// NOTE: ordering is intentionally NOT weight-driven in this phase. The weight is
// recorded so the explanation and Part 2's scoring have it available, but the
// returned order matches the input order.
func (s *WeightedStrategy) Rank(_ context.Context, _ RoutingContext, candidates []Candidate) ([]Candidate, error) {
	out := make([]Candidate, len(candidates))
	copy(out, candidates)
	for i := range out {
		out[i].Weight = s.weightFor(out[i].Provider)
		// Score is left at zero: scoring is Phase 2 Part 2.
		out[i].Reason = "weighted strategy (weights attached; scoring pending Phase 2 Part 2)"
	}
	return out, nil
}

// weightFor returns the configured weight for a provider, or the default.
func (s *WeightedStrategy) weightFor(providerName string) float64 {
	if w, ok := s.cfg.Weights[providerName]; ok {
		return w
	}
	return s.cfg.DefaultWeight
}
