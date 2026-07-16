package shadow

import (
	"context"
	"fmt"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Decision is a sampling verdict for one request.
type Decision struct {
	// Sample reports whether the request should generate shadow traffic.
	Sample bool
	// Rate is the effective sampling percentage that produced the verdict.
	Rate float64
	// Reason is a short human-readable explanation.
	Reason string
}

// Policy decides whether a given request should be shadowed. It is pluggable so
// new sampling strategies (rule-based, per-tenant, complexity-aware) can be added
// without changing the Manager. A policy must be safe for concurrent use.
type Policy interface {
	// Name returns the stable policy identifier.
	Name() string
	// Decide returns the sampling verdict for a request.
	Decide(ctx context.Context, req provider.ChatRequest) Decision
}

// DisabledPolicy never samples. It is the safe default.
type DisabledPolicy struct{}

// Name returns the policy identifier.
func (DisabledPolicy) Name() string { return PolicyDisabled }

// Decide never samples.
func (DisabledPolicy) Decide(context.Context, provider.ChatRequest) Decision {
	return Decision{Sample: false, Rate: 0, Reason: "shadow traffic disabled"}
}

// FixedPercentagePolicy samples a fixed percentage of requests uniformly at
// random. The random source is injected so sampling is deterministic in tests.
type FixedPercentagePolicy struct {
	pct    float64
	sample func() float64 // returns a value in [0,1)
}

// NewFixedPercentagePolicy constructs a fixed-percentage policy. A nil sampler
// falls back to the package's default (locked math/rand) source.
func NewFixedPercentagePolicy(pct float64, sampler func() float64) *FixedPercentagePolicy {
	if sampler == nil {
		sampler = defaultSampler()
	}
	return &FixedPercentagePolicy{pct: pct, sample: sampler}
}

// Name returns the policy identifier.
func (p *FixedPercentagePolicy) Name() string { return PolicyFixedPercentage }

// Decide samples with probability pct/100.
func (p *FixedPercentagePolicy) Decide(context.Context, provider.ChatRequest) Decision {
	if p.pct <= 0 {
		return Decision{Sample: false, Rate: p.pct, Reason: "0% sampling"}
	}
	if p.pct >= 100 {
		return Decision{Sample: true, Rate: 100, Reason: "100% sampling"}
	}
	r := p.sample()
	sampled := r < p.pct/100
	return Decision{Sample: sampled, Rate: p.pct, Reason: fmt.Sprintf("fixed %.0f%% sampling", p.pct)}
}

// PolicyBuilder constructs a Policy from configuration and a sampler.
type PolicyBuilder func(cfg Config, sampler func() float64) (Policy, error)

// PolicyRegistry maps policy names to builders — the extension seam for new
// sampling strategies.
type PolicyRegistry struct {
	builders map[string]PolicyBuilder
}

// NewPolicyRegistry returns an empty policy registry.
func NewPolicyRegistry() *PolicyRegistry {
	return &PolicyRegistry{builders: make(map[string]PolicyBuilder)}
}

// Register adds a builder under name, overwriting any existing entry.
func (r *PolicyRegistry) Register(name string, b PolicyBuilder) { r.builders[name] = b }

// Build instantiates the named policy, distinguishing reserved-but-unimplemented
// names from unknown ones.
func (r *PolicyRegistry) Build(name string, cfg Config, sampler func() float64) (Policy, error) {
	if b, ok := r.builders[name]; ok {
		return b(cfg, sampler)
	}
	if isReserved(name) {
		return nil, fmt.Errorf("%w: %q", ErrPolicyNotImplemented, name)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownPolicy, name)
}

// DefaultPolicyRegistry returns a registry with the implemented policies.
func DefaultPolicyRegistry() *PolicyRegistry {
	r := NewPolicyRegistry()
	r.Register(PolicyDisabled, func(Config, func() float64) (Policy, error) { return DisabledPolicy{}, nil })
	r.Register(PolicyFixedPercentage, func(cfg Config, sampler func() float64) (Policy, error) {
		return NewFixedPercentagePolicy(cfg.Percentage, sampler), nil
	})
	return r
}
