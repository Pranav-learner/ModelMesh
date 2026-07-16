package loadbalancer

import "github.com/symbiotes/modelmesh/internal/provider"

// Metrics is the optional observability seam for the balancer. It mirrors the
// routing engine's approach: the balancer depends on this small interface, not on
// Prometheus, so it is observable without any metrics backend wired and the
// composition layer can bridge it into the metrics catalog later.
type Metrics interface {
	// RecordSelection is called once per successful Select with the strategy used
	// and the chosen instance's provider and ID.
	RecordSelection(strategy, providerName, instanceID string)
}

// NopMetrics is the no-op Metrics used by default.
type NopMetrics struct{}

// RecordSelection does nothing.
func (NopMetrics) RecordSelection(string, string, string) {}

// HealthSource is the optional seam through which the balancer learns provider
// health and gates unhealthy providers out of selection. Its signature matches
// resilience.Registry.Health, so *resilience.Registry can be injected directly
// without either package importing the other.
type HealthSource interface {
	Health(providerName string) (provider.HealthStatus, bool)
}
