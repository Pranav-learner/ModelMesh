// Package loadbalancer distributes requests across multiple provider instances.
//
// Where the Routing Engine decides which logical provider/model should serve a
// request, the load balancer decides which concrete instance of a provider
// receives it — e.g. OpenAI region us-east-1 vs eu-west-1 vs us-west-2. The two
// compose: routing picks the provider, the balancer picks the instance.
//
// # Design
//
// The subsystem is built around three small, orthogonal pieces so that new
// balancing algorithms plug in without touching existing code:
//
//   - Instance / InstanceRegistry — the set of routable instances and their live
//     runtime state (health, rolling latency, request count, last used).
//   - Strategy — the pluggable selection algorithm (Round Robin, Least Latency;
//     Weighted Round Robin, Least Connections, Random, and Consistent Hashing are
//     reserved extension points). A strategy is pure with respect to the candidate
//     snapshot it is handed; the balancer owns candidate enumeration and feedback.
//   - LoadBalancer — the façade (Select / Register / Remove / Update /
//     Statistics) that wires a registry to a strategy and closes the feedback
//     loop.
//
// # Selection pipeline
//
//	Request → LoadBalancer → filter (enabled + healthy) → Strategy → Instance → Provider
//	                                                                     │
//	                          Update(latency, health) ←──── dispatch ────┘
//
// # Integration
//
// The balancer integrates with the rest of ModelMesh through narrow seams, never
// reaching into other packages' internals:
//
//   - Provider Layer: an Instance may carry the concrete provider.LLMProvider it
//     fronts, so the selected instance is directly dispatchable.
//   - Resilience Layer: an optional HealthSource (structurally satisfied by
//     resilience.Registry) gates unhealthy providers out of selection.
//   - Observability: an optional Metrics sink records selections. Rolling latency
//     is tracked internally (not via Prometheus), so the balancer is observable
//     without any metrics backend wired.
package loadbalancer
