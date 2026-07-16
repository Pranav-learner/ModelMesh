package metrics

import "time"

// Circuit breaker state codes exposed by the circuit_state gauge. The resilience
// package maps its State to these codes when recording, so metrics stays a leaf.
const (
	CircuitClosedCode   = 0.0
	CircuitOpenCode     = 1.0
	CircuitHalfOpenCode = 2.0
)

// durationBuckets are second-scale latency buckets suited to LLM request timing.
var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// Subsystem recorder interfaces. Each subsystem depends on the small interface it
// needs, never on Prometheus or the full catalog.

// GatewayRecorder records end-to-end request metrics.
type GatewayRecorder interface {
	GatewayRequest(success bool, duration time.Duration)
}

// RouterRecorder records routing decision metrics.
type RouterRecorder interface {
	RoutingDecision(providerName string, duration time.Duration)
}

// CacheRecorder records cache metrics.
type CacheRecorder interface {
	CacheHit(level string)
	CacheMiss()
	AddTokensSaved(n int)
	AddCostSaved(usd float64)
}

// ProviderRecorder records provider call metrics.
type ProviderRecorder interface {
	ProviderRequest(providerName string, success bool, latency time.Duration)
}

// BreakerRecorder records circuit breaker and failover metrics.
type BreakerRecorder interface {
	CircuitStateChange(providerName, to string)
	SetCircuitState(providerName string, code float64)
	SetOpenCircuits(n int)
	Failover()
}

// HealthRecorder records provider health gauges.
type HealthRecorder interface {
	SetProvidersHealthy(n int)
	SetProvidersUnhealthy(n int)
}

// Recorder is the union of every subsystem recorder. *Metrics and NoOp implement
// it; subsystems should depend on the narrow interface they need.
type Recorder interface {
	GatewayRecorder
	RouterRecorder
	CacheRecorder
	ProviderRecorder
	BreakerRecorder
	HealthRecorder
}

// Compile-time assertions.
var (
	_ Recorder = (*Metrics)(nil)
	_ Recorder = NoOp{}
)

// Metrics is the typed catalog of ModelMesh's core metrics. It is created from a
// Manager (which registers each collector) and exposes recorder methods.
type Metrics struct {
	// Gateway
	gatewayRequests CounterVec // labels: outcome
	gatewayDuration Histogram

	// Router
	routingDecisions CounterVec // labels: provider
	routingDuration  Histogram

	// Cache
	cacheHits   CounterVec // labels: level
	cacheMisses Counter
	tokensSaved Counter
	costSaved   Counter

	// Providers
	providerRequests CounterVec   // labels: provider, outcome
	providerErrors   CounterVec   // labels: provider
	providerDuration HistogramVec // labels: provider

	// Circuit breaker
	circuitStateChanges CounterVec // labels: provider, to
	circuitState        GaugeVec   // labels: provider
	openCircuits        Gauge
	failovers           Counter

	// Health
	providersHealthy   Gauge
	providersUnhealthy Gauge
}

// New registers ModelMesh's core metrics with the manager and returns the catalog.
func New(m *Manager) *Metrics {
	return &Metrics{
		gatewayRequests: m.CounterVec("gateway_requests_total", "Total gateway requests by outcome.", []string{"outcome"}),
		gatewayDuration: m.Histogram("gateway_request_duration_seconds", "Gateway request duration in seconds.", durationBuckets),

		routingDecisions: m.CounterVec("routing_decisions_total", "Routing decisions by selected provider.", []string{"provider"}),
		routingDuration:  m.Histogram("routing_decision_duration_seconds", "Routing decision duration in seconds.", durationBuckets),

		cacheHits:   m.CounterVec("cache_hits_total", "Cache hits by level (l1/l2/l3).", []string{"level"}),
		cacheMisses: m.Counter("cache_misses_total", "Cache misses (all levels)."),
		tokensSaved: m.Counter("cache_tokens_saved_total", "Tokens saved by cache hits."),
		costSaved:   m.Counter("cache_cost_saved_usd_total", "Estimated USD cost saved by cache hits."),

		providerRequests: m.CounterVec("provider_requests_total", "Provider requests by provider and outcome.", []string{"provider", "outcome"}),
		providerErrors:   m.CounterVec("provider_errors_total", "Provider errors by provider.", []string{"provider"}),
		providerDuration: m.HistogramVec("provider_request_duration_seconds", "Provider request latency in seconds.", []string{"provider"}, durationBuckets),

		circuitStateChanges: m.CounterVec("circuit_state_changes_total", "Circuit breaker state changes by provider and target state.", []string{"provider", "to"}),
		circuitState:        m.GaugeVec("circuit_state", "Circuit breaker state per provider (0 closed, 1 open, 2 half-open).", []string{"provider"}),
		openCircuits:        m.Gauge("circuit_open_circuits", "Number of currently-open circuit breakers."),
		failovers:           m.Counter("failovers_total", "Total automatic failovers."),

		providersHealthy:   m.Gauge("providers_healthy", "Number of healthy providers."),
		providersUnhealthy: m.Gauge("providers_unhealthy", "Number of unhealthy providers."),
	}
}

// --- recorder methods ---

// GatewayRequest records a completed gateway request and its duration.
func (m *Metrics) GatewayRequest(success bool, duration time.Duration) {
	m.gatewayRequests.With(outcome(success)).Inc()
	m.gatewayDuration.Observe(duration.Seconds())
}

// RoutingDecision records a routing decision and its duration.
func (m *Metrics) RoutingDecision(providerName string, duration time.Duration) {
	m.routingDecisions.With(providerName).Inc()
	m.routingDuration.Observe(duration.Seconds())
}

// CacheHit records a cache hit at the given level.
func (m *Metrics) CacheHit(level string) { m.cacheHits.With(level).Inc() }

// CacheMiss records a cache miss.
func (m *Metrics) CacheMiss() { m.cacheMisses.Inc() }

// AddTokensSaved adds to the tokens-saved counter.
func (m *Metrics) AddTokensSaved(n int) {
	if n > 0 {
		m.tokensSaved.Add(float64(n))
	}
}

// AddCostSaved adds to the cost-saved counter.
func (m *Metrics) AddCostSaved(usd float64) {
	if usd > 0 {
		m.costSaved.Add(usd)
	}
}

// ProviderRequest records a provider call, its outcome, and latency.
func (m *Metrics) ProviderRequest(providerName string, success bool, latency time.Duration) {
	m.providerRequests.With(providerName, outcome(success)).Inc()
	if !success {
		m.providerErrors.With(providerName).Inc()
	}
	m.providerDuration.With(providerName).Observe(latency.Seconds())
}

// CircuitStateChange records a breaker transition to the named state.
func (m *Metrics) CircuitStateChange(providerName, to string) {
	m.circuitStateChanges.With(providerName, to).Inc()
}

// SetCircuitState sets the current breaker state gauge for a provider.
func (m *Metrics) SetCircuitState(providerName string, code float64) {
	m.circuitState.With(providerName).Set(code)
}

// SetOpenCircuits sets the count of currently-open breakers.
func (m *Metrics) SetOpenCircuits(n int) { m.openCircuits.Set(float64(n)) }

// Failover records one automatic failover.
func (m *Metrics) Failover() { m.failovers.Inc() }

// SetProvidersHealthy sets the healthy-provider gauge.
func (m *Metrics) SetProvidersHealthy(n int) { m.providersHealthy.Set(float64(n)) }

// SetProvidersUnhealthy sets the unhealthy-provider gauge.
func (m *Metrics) SetProvidersUnhealthy(n int) { m.providersUnhealthy.Set(float64(n)) }

func outcome(success bool) string {
	if success {
		return "success"
	}
	return "error"
}

// NoOp is a Recorder that discards everything. It is the safe default when
// metrics are disabled, so subsystems never need a nil check.
type NoOp struct{}

func (NoOp) GatewayRequest(bool, time.Duration)          {}
func (NoOp) RoutingDecision(string, time.Duration)       {}
func (NoOp) CacheHit(string)                             {}
func (NoOp) CacheMiss()                                  {}
func (NoOp) AddTokensSaved(int)                          {}
func (NoOp) AddCostSaved(float64)                        {}
func (NoOp) ProviderRequest(string, bool, time.Duration) {}
func (NoOp) CircuitStateChange(string, string)           {}
func (NoOp) SetCircuitState(string, float64)             {}
func (NoOp) SetOpenCircuits(int)                         {}
func (NoOp) Failover()                                   {}
func (NoOp) SetProvidersHealthy(int)                     {}
func (NoOp) SetProvidersUnhealthy(int)                   {}
