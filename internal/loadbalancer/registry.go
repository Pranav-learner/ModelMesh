package loadbalancer

import (
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// managedInstance is the registry's mutable runtime state for one instance. All
// access is guarded by InstanceRegistry.mu.
type managedInstance struct {
	desc     Instance
	enabled  bool
	health   provider.HealthState
	latency  *rollingLatency
	requests uint64
	lastUsed time.Time
}

func (m *managedInstance) snapshot() InstanceStats {
	return InstanceStats{
		ID:             m.desc.ID,
		Provider:       m.desc.Provider,
		Region:         m.desc.Region,
		Weight:         m.desc.Weight,
		Enabled:        m.enabled,
		Health:         m.health,
		Healthy:        m.enabled && m.health != provider.HealthStateUnhealthy,
		AverageLatency: m.latency.average(),
		Samples:        m.latency.samples(),
		RequestCount:   m.requests,
		LastUsed:       m.lastUsed,
	}
}

// InstanceRegistry maintains the set of active provider instances and their live
// runtime state. It is safe for concurrent use and is the single source of truth
// the balancer selects from. Instances start enabled and assumed healthy.
type InstanceRegistry struct {
	mu            sync.RWMutex
	instances     map[string]*managedInstance
	order         []string // insertion-ordered IDs, kept stable for deterministic listing
	latencyWindow int
	clock         func() time.Time
}

// NewInstanceRegistry returns an empty registry using the given rolling-latency
// window size and clock. A non-positive window falls back to the default; a nil
// clock falls back to time.Now.
func NewInstanceRegistry(latencyWindow int, clock func() time.Time) *InstanceRegistry {
	if latencyWindow <= 0 {
		latencyWindow = DefaultLatencyWindow
	}
	if clock == nil {
		clock = time.Now
	}
	return &InstanceRegistry{
		instances:     make(map[string]*managedInstance),
		latencyWindow: latencyWindow,
		clock:         clock,
	}
}

// Register adds a new instance. It returns ErrInvalidInstance if required fields
// are missing and ErrInstanceExists if the ID is already registered.
func (r *InstanceRegistry) Register(inst Instance) error {
	if !inst.valid() {
		return ErrInvalidInstance
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.instances[inst.ID]; ok {
		return ErrInstanceExists
	}
	r.instances[inst.ID] = &managedInstance{
		desc:    inst,
		enabled: true,
		health:  provider.HealthStateHealthy,
		latency: newRollingLatency(r.latencyWindow),
	}
	r.order = append(r.order, inst.ID)
	return nil
}

// Deregister removes an instance, discarding its runtime state. It returns
// ErrInstanceNotFound for an unknown ID.
func (r *InstanceRegistry) Deregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.instances[id]; !ok {
		return ErrInstanceNotFound
	}
	delete(r.instances, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// Enable marks an instance eligible for selection.
func (r *InstanceRegistry) Enable(id string) error { return r.setEnabled(id, true) }

// Disable marks an instance ineligible for selection without removing it, so its
// accumulated stats are preserved for when it is re-enabled.
func (r *InstanceRegistry) Disable(id string) error { return r.setEnabled(id, false) }

func (r *InstanceRegistry) setEnabled(id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.instances[id]
	if !ok {
		return ErrInstanceNotFound
	}
	m.enabled = enabled
	return nil
}

// SetHealth updates an instance's health state (e.g. from an out-of-band probe).
func (r *InstanceRegistry) SetHealth(id string, state provider.HealthState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.instances[id]
	if !ok {
		return ErrInstanceNotFound
	}
	m.health = state
	return nil
}

// Discover reconciles the registry against a desired set of instances: new IDs
// are registered, absent IDs are deregistered, and existing IDs are retained with
// their runtime state (and refreshed descriptor). It supports service-discovery
// style refresh where the instance set changes over time.
func (r *InstanceRegistry) Discover(desired []Instance) error {
	for _, inst := range desired {
		if !inst.valid() {
			return ErrInvalidInstance
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	want := make(map[string]Instance, len(desired))
	for _, inst := range desired {
		want[inst.ID] = inst
	}
	// Remove instances no longer desired.
	kept := r.order[:0:0]
	for _, id := range r.order {
		if _, ok := want[id]; ok {
			kept = append(kept, id)
			continue
		}
		delete(r.instances, id)
	}
	r.order = kept
	// Add or refresh desired instances.
	for _, inst := range desired {
		if m, ok := r.instances[inst.ID]; ok {
			m.desc = inst // refresh descriptor, preserve stats
			continue
		}
		r.instances[inst.ID] = &managedInstance{
			desc:    inst,
			enabled: true,
			health:  provider.HealthStateHealthy,
			latency: newRollingLatency(r.latencyWindow),
		}
		r.order = append(r.order, inst.ID)
	}
	return nil
}

// Update records a completed request's observation against its instance: it adds
// the latency sample and, if the observation carries a health state, updates it.
// It returns ErrInstanceNotFound for an unknown ID.
func (r *InstanceRegistry) Update(obs Observation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.instances[obs.InstanceID]
	if !ok {
		return ErrInstanceNotFound
	}
	if obs.Latency > 0 {
		m.latency.record(obs.Latency)
	}
	if obs.Health != "" {
		m.health = obs.Health
	}
	return nil
}

// markSelected increments an instance's request count and stamps LastUsed,
// returning its post-update snapshot. The bool is false if the instance was
// removed since candidate enumeration.
func (r *InstanceRegistry) markSelected(id string) (InstanceStats, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.instances[id]
	if !ok {
		return InstanceStats{}, false
	}
	m.requests++
	m.lastUsed = r.clock()
	return m.snapshot(), true
}

// descriptor returns an instance's immutable descriptor.
func (r *InstanceRegistry) descriptor(id string) (Instance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.instances[id]
	if !ok {
		return Instance{}, false
	}
	return m.desc, true
}

// List returns a snapshot of every instance's stats in stable insertion order.
func (r *InstanceRegistry) List() []InstanceStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]InstanceStats, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.instances[id].snapshot())
	}
	return out
}

// Len returns the number of registered instances.
func (r *InstanceRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.instances)
}
