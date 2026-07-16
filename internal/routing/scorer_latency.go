package routing

import (
	"context"
	"time"
)

// LatencyScorer scores candidates by expected latency, preferring faster
// providers. Latencies are configured (not live) in this phase; live metrics
// arrive with the Observability phase, at which point a metrics-backed
// LatencySource can replace the configured values without changing this scorer's
// contract.
//
// Responsibility: latency only.
type LatencyScorer struct {
	cfg LatencyConfig
}

// NewLatencyScorer constructs a latency scorer, applying config defaults.
func NewLatencyScorer(cfg LatencyConfig) *LatencyScorer {
	return &LatencyScorer{cfg: cfg.withDefaults()}
}

// Name returns the scorer identifier.
func (s *LatencyScorer) Name() string { return ScorerLatency }

// Scores reads each candidate's expected latency and normalizes (lower latency ->
// higher score) across the candidate set.
func (s *LatencyScorer) Scores(_ context.Context, _ RoutingContext, candidates []Candidate) ([]float64, error) {
	latencies := make([]float64, len(candidates))
	for i, c := range candidates {
		latencies[i] = s.ExpectedLatency(c).Seconds()
	}
	return normalizeLowerIsBetter(latencies), nil
}

// ExpectedLatency returns the configured expected latency for a candidate. A
// model-specific value takes precedence over a provider value, which takes
// precedence over the default.
func (s *LatencyScorer) ExpectedLatency(c Candidate) time.Duration {
	if d, ok := s.cfg.Models[c.Model]; ok {
		return d
	}
	if d, ok := s.cfg.Providers[c.Provider]; ok {
		return d
	}
	return s.cfg.Default
}
