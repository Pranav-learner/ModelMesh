package routing

import "context"

// CostScorer scores candidates by estimated request cost, rewarding cheaper
// providers. It estimates each candidate's cost from configured pricing and token
// estimates, then normalizes so the cheapest candidate scores 1.0.
//
// Responsibility: cost only. It holds no latency, quality, or health knowledge.
type CostScorer struct {
	cfg CostConfig
}

// NewCostScorer constructs a cost scorer, applying config defaults.
func NewCostScorer(cfg CostConfig) *CostScorer {
	return &CostScorer{cfg: cfg.withDefaults()}
}

// Name returns the scorer identifier.
func (s *CostScorer) Name() string { return ScorerCost }

// Scores estimates each candidate's cost and normalizes (lower cost -> higher
// score) across the candidate set.
func (s *CostScorer) Scores(_ context.Context, rc RoutingContext, candidates []Candidate) ([]float64, error) {
	costs := make([]float64, len(candidates))
	for i, c := range candidates {
		costs[i] = s.EstimateCost(rc, c)
	}
	return normalizeLowerIsBetter(costs), nil
}

// EstimateCost returns the estimated cost of serving rc with candidate c, using
// the candidate's pricing and the request's (or default) token estimates. It is
// exported so tests and future phases can reuse the estimate.
func (s *CostScorer) EstimateCost(rc RoutingContext, c Candidate) float64 {
	pricing := s.pricingFor(c.Model)
	inTokens := estimatedTokens(rc, AttrEstimatedInputTokens, s.cfg.EstimatedInputTokens)
	outTokens := estimatedTokens(rc, AttrEstimatedOutputTokens, s.cfg.EstimatedOutputTokens)
	return perThousand(inTokens)*pricing.InputPer1K + perThousand(outTokens)*pricing.OutputPer1K
}

// pricingFor returns the pricing for a model, falling back to the default.
func (s *CostScorer) pricingFor(model string) ModelPricing {
	if p, ok := s.cfg.Pricing[model]; ok {
		return p
	}
	return s.cfg.Default
}

func perThousand(tokens int) float64 { return float64(tokens) / 1000.0 }
