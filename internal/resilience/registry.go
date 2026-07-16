package resilience

import (
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// HealthRecord is the live health information the monitor maintains for one
// provider. It combines the circuit breaker's state with probe measurements.
type HealthRecord struct {
	Provider string `json:"provider"`
	// State is the provider's circuit breaker state at the last probe.
	State State `json:"state"`
	// Available reports whether the provider is routable (breaker not open).
	Available bool `json:"available"`
	// Latency is the most recent probe latency.
	Latency time.Duration `json:"latency"`
	// LastSuccess / LastFailure are the times of the last successful and failed
	// probe. LastError is the last probe error message.
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	// CheckedAt is when the record was last updated.
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

// Registry maintains live health records for every provider. It is safe for
// concurrent use and is the single source of truth the router reads from. Its
// Health method structurally matches the routing engine's health-provider seam,
// so it can be injected into routing without either package importing the other.
type Registry struct {
	mu      sync.RWMutex
	records map[string]HealthRecord
}

// NewRegistry returns an empty health registry.
func NewRegistry() *Registry {
	return &Registry{records: make(map[string]HealthRecord)}
}

// set stores a record (internal; used by the monitor).
func (r *Registry) set(rec HealthRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[rec.Provider] = rec
}

// Record returns the current health record for a provider.
func (r *Registry) Record(providerName string) (HealthRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[providerName]
	return rec, ok
}

// Records returns a snapshot of every provider's health record.
func (r *Registry) Records() map[string]HealthRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]HealthRecord, len(r.records))
	for name, rec := range r.records {
		out[name] = rec
	}
	return out
}

// Health returns the provider's health as a normalized provider.HealthStatus and
// whether it is known. This signature matches the routing engine's HealthProvider
// interface, so *Registry can be passed to routing.WithHealthProvider directly.
func (r *Registry) Health(providerName string) (provider.HealthStatus, bool) {
	rec, ok := r.Record(providerName)
	if !ok {
		return provider.HealthStatus{}, false
	}
	return provider.HealthStatus{
		Provider:  providerName,
		State:     healthState(rec.State),
		Detail:    rec.LastError,
		CheckedAt: rec.CheckedAt,
		Latency:   rec.Latency,
	}, true
}

// healthState maps a circuit breaker state to a provider health state:
// closed -> healthy, half-open -> degraded, open -> unhealthy.
func healthState(s State) provider.HealthState {
	switch s {
	case StateClosed:
		return provider.HealthStateHealthy
	case StateHalfOpen:
		return provider.HealthStateDegraded
	case StateOpen:
		return provider.HealthStateUnhealthy
	default:
		return provider.HealthStateUnknown
	}
}
