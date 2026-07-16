package loadbalancer

// Statistics is a point-in-time view of the balancer pool: the active strategy,
// pool composition (total / enabled / routable), the running selection total, and
// per-instance stats. It is a read model — safe to serialize and log.
type Statistics struct {
	Strategy        string          `json:"strategy"`
	TotalInstances  int             `json:"total_instances"`
	EnabledCount    int             `json:"enabled_count"`
	HealthyCount    int             `json:"healthy_count"` // currently routable
	TotalSelections uint64          `json:"total_selections"`
	Instances       []InstanceStats `json:"instances"`
}

// Statistics returns a snapshot of the pool. The per-instance Healthy flag and
// the HealthyCount reflect full routability (enabled + instance health + provider
// health via any wired HealthSource), so they match what Select would consider
// eligible right now.
func (b *Balancer) Statistics() Statistics {
	instances := b.registry.List()
	stats := Statistics{
		Strategy:        b.strategy.Name(),
		TotalInstances:  len(instances),
		TotalSelections: b.selections.Load(),
		Instances:       instances,
	}
	for i := range stats.Instances {
		s := &stats.Instances[i]
		if s.Enabled {
			stats.EnabledCount++
		}
		s.Healthy = b.eligible(*s) // refine registry-local Healthy with provider health
		if s.Healthy {
			stats.HealthyCount++
		}
	}
	return stats
}
