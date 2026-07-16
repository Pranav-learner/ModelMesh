package shadow

import "sort"

// Selector chooses the secondary provider for a shadow request, independently of
// the primary response. It is pluggable so smarter selection (round-robin,
// weighted, capability-aware) can be added without changing the Manager.
type Selector interface {
	// Name returns the stable selector identifier.
	Name() string
	// Select chooses a secondary target from the candidates (which exclude nothing;
	// the selector is responsible for not choosing the primary). It returns false
	// when no suitable secondary exists.
	Select(primary Target, candidates []Target) (Target, bool)
}

// FirstOtherSelector picks the first candidate (by provider name) whose provider
// differs from the primary. Deterministic — the same inputs always yield the same
// secondary — which keeps shadow selection reproducible.
type FirstOtherSelector struct{}

// Name returns the selector identifier.
func (FirstOtherSelector) Name() string { return "first_other" }

// Select returns the lowest-named provider that is not the primary.
func (FirstOtherSelector) Select(primary Target, candidates []Target) (Target, bool) {
	sorted := make([]Target, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Provider < sorted[j].Provider })
	for _, c := range sorted {
		if c.Provider != primary.Provider {
			return c, true
		}
	}
	return Target{}, false
}
