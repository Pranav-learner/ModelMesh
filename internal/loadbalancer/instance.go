package loadbalancer

import (
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Instance describes a single routable provider endpoint. Multiple instances can
// back one logical provider (e.g. OpenAI region us-east-1 / eu-west-1 /
// us-west-2). It is an immutable descriptor; live runtime state (health, latency,
// counts) is maintained by the InstanceRegistry and reported via InstanceStats.
type Instance struct {
	// ID uniquely identifies the instance within the balancer. Required.
	ID string `json:"id"`
	// Provider is the logical provider name this instance belongs to (e.g.
	// "openai"). Required; used by health gating and the request provider filter.
	Provider string `json:"provider"`
	// Region is the deployment region/zone (e.g. "us-east-1"). Optional label.
	Region string `json:"region,omitempty"`
	// Weight is a relative selection weight, reserved for weighted strategies. A
	// zero value is treated as the default weight by strategies that use it.
	Weight float64 `json:"weight,omitempty"`
	// Client is the concrete provider client this instance fronts. It is optional:
	// when set, the selected instance is directly dispatchable; when nil, the
	// caller resolves the provider by name. The balancer never calls it.
	Client provider.LLMProvider `json:"-"`
}

// valid reports whether the instance carries the required fields.
func (i Instance) valid() bool { return i.ID != "" && i.Provider != "" }

// InstanceStats is a point-in-time snapshot of one instance's descriptor and
// runtime state. It is what Strategy candidates and Statistics are built from.
type InstanceStats struct {
	ID       string               `json:"id"`
	Provider string               `json:"provider"`
	Region   string               `json:"region,omitempty"`
	Weight   float64              `json:"weight,omitempty"`
	Enabled  bool                 `json:"enabled"`
	Health   provider.HealthState `json:"health"`
	// Healthy reports whether the instance is currently routable (enabled, not
	// unhealthy, and — when a HealthSource is wired — its provider is not
	// unhealthy). It is computed by the balancer, so registry-level snapshots set
	// it from local state only.
	Healthy bool `json:"healthy"`
	// AverageLatency is the rolling mean latency over the most recent requests
	// (the "current latency" of the instance). Zero when no samples exist.
	AverageLatency time.Duration `json:"average_latency"`
	// Samples is how many latency observations back AverageLatency.
	Samples int `json:"samples"`
	// RequestCount is the total number of times this instance has been selected.
	RequestCount uint64 `json:"request_count"`
	// LastUsed is when this instance was last selected (zero if never).
	LastUsed time.Time `json:"last_used,omitempty"`
}

// Request is the selection input handed to Select. All fields are optional.
type Request struct {
	// Provider, when set, restricts selection to instances of this provider. Empty
	// selects across all providers' instances.
	Provider string
	// Key is a stable partition key used by key-based strategies (reserved for
	// Consistent Hashing). Ignored by Round Robin and Least Latency.
	Key string
}

// Observation is the post-dispatch feedback fed to Update, closing the loop so
// latency-aware strategies see real measurements.
type Observation struct {
	// InstanceID identifies which instance served the request. Required.
	InstanceID string
	// Latency is the observed request latency to record in the rolling window.
	Latency time.Duration
	// Success reports whether the request succeeded. Recorded for statistics.
	Success bool
	// Health, when non-empty, updates the instance's health state (e.g. from an
	// out-of-band probe). Empty leaves the current health unchanged.
	Health provider.HealthState
}

// Selection is the result of Select: the chosen instance plus the strategy used
// and the instance's stats at selection time.
type Selection struct {
	Instance Instance      `json:"instance"`
	Strategy string        `json:"strategy"`
	Stats    InstanceStats `json:"stats"`
}
