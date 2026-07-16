package routing

import (
	"context"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// HealthProvider supplies the last-known health of a provider to the availability
// scorer. It is the extension seam to the future Health Monitoring phase: this
// phase ships NoHealth (all unknown), and Phase 4 will provide a monitor-backed
// implementation without changing the scorer.
type HealthProvider interface {
	// Health returns the last-known health of a provider and whether it is known.
	Health(providerName string) (provider.HealthStatus, bool)
}

// NoHealth is a HealthProvider that never knows a provider's health, so the
// availability scorer falls back to the configured "unknown" score.
type NoHealth struct{}

// Health always reports unknown.
func (NoHealth) Health(string) (provider.HealthStatus, bool) {
	return provider.HealthStatus{}, false
}

// AvailabilityScorer scores candidates by provider availability, derived from
// health state. Scores are already in [0,1] (health-state → score mapping), so no
// cross-candidate normalization is applied.
//
// Responsibility: availability only.
type AvailabilityScorer struct {
	cfg    AvailabilityConfig
	health HealthProvider
}

// NewAvailabilityScorer constructs an availability scorer. A nil HealthProvider
// defaults to NoHealth, making all providers score as "unknown".
func NewAvailabilityScorer(cfg AvailabilityConfig, health HealthProvider) *AvailabilityScorer {
	if health == nil {
		health = NoHealth{}
	}
	return &AvailabilityScorer{cfg: cfg.withDefaults(), health: health}
}

// Name returns the scorer identifier.
func (s *AvailabilityScorer) Name() string { return ScorerAvailability }

// Scores returns the availability score of each candidate's provider.
func (s *AvailabilityScorer) Scores(_ context.Context, _ RoutingContext, candidates []Candidate) ([]float64, error) {
	scores := make([]float64, len(candidates))
	for i, c := range candidates {
		scores[i] = s.score(c.Provider)
	}
	return scores, nil
}

// score resolves a provider's availability: a static override wins; otherwise the
// last-known health state maps to a configured score; otherwise "unknown".
func (s *AvailabilityScorer) score(providerName string) float64 {
	if v, ok := s.cfg.Overrides[providerName]; ok {
		return clamp01(v)
	}
	if h, ok := s.health.Health(providerName); ok {
		return clamp01(s.stateScore(h.State))
	}
	return clamp01(s.cfg.Unknown)
}

func (s *AvailabilityScorer) stateScore(state provider.HealthState) float64 {
	switch state {
	case provider.HealthStateHealthy:
		return s.cfg.Healthy
	case provider.HealthStateDegraded:
		return s.cfg.Degraded
	case provider.HealthStateUnhealthy:
		return s.cfg.Unhealthy
	default: // HealthStateUnknown or any unrecognized state
		return s.cfg.Unknown
	}
}
