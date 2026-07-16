package routing

import "context"

// QualityScorer scores candidates by a configured model quality value in [0,1].
// Quality is configuration, never hardcoded into logic, so operators can tune it
// without recompiling. Scores are already normalized, so no cross-candidate
// normalization is applied.
//
// Responsibility: quality only.
type QualityScorer struct {
	cfg QualityConfig
}

// NewQualityScorer constructs a quality scorer, applying config defaults.
func NewQualityScorer(cfg QualityConfig) *QualityScorer {
	return &QualityScorer{cfg: cfg.withDefaults()}
}

// Name returns the scorer identifier.
func (s *QualityScorer) Name() string { return ScorerQuality }

// Scores returns the configured quality of each candidate's model.
func (s *QualityScorer) Scores(_ context.Context, _ RoutingContext, candidates []Candidate) ([]float64, error) {
	scores := make([]float64, len(candidates))
	for i, c := range candidates {
		scores[i] = s.quality(c)
	}
	return scores, nil
}

// quality resolves a candidate's quality: a model value takes precedence over a
// provider value, which takes precedence over the default.
func (s *QualityScorer) quality(c Candidate) float64 {
	if q, ok := s.cfg.Models[c.Model]; ok {
		return clamp01(q)
	}
	if q, ok := s.cfg.Providers[c.Provider]; ok {
		return clamp01(q)
	}
	return clamp01(s.cfg.Default)
}
