package observability

import (
	"github.com/symbiotes/modelmesh/internal/metrics"
	"github.com/symbiotes/modelmesh/internal/resilience"
)

// stateCode maps a circuit breaker state to its metrics gauge code.
func stateCode(s resilience.State) float64 {
	switch s {
	case resilience.StateOpen:
		return metrics.CircuitOpenCode
	case resilience.StateHalfOpen:
		return metrics.CircuitHalfOpenCode
	default:
		return metrics.CircuitClosedCode
	}
}

// Publish sets the circuit-state, open-circuit, and provider-health gauges from
// the current breaker snapshot. Because gauges reflect instantaneous state, this
// is called after each health probe round (or on an interval), not per request.
// The breaker manager is the source of truth: a provider is healthy unless its
// circuit is open.
func Publish(rec metrics.Recorder, breakers *resilience.Manager) {
	states := breakers.States()
	open, healthy, unhealthy := 0, 0, 0
	for name, state := range states {
		rec.SetCircuitState(name, stateCode(state))
		if state == resilience.StateOpen {
			open++
			unhealthy++
		} else {
			healthy++
		}
	}
	rec.SetOpenCircuits(open)
	rec.SetProvidersHealthy(healthy)
	rec.SetProvidersUnhealthy(unhealthy)
}

// BreakerListener returns a resilience health-event listener that records circuit
// state-change counters. Register it with a Monitor via AddListener so every
// transition increments the metric.
func BreakerListener(rec metrics.BreakerRecorder) resilience.Listener {
	return func(e resilience.Event) {
		if e.Type == resilience.EventStateChanged {
			rec.CircuitStateChange(e.Provider, e.To.String())
		}
	}
}
