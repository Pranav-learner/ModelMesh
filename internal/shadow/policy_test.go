package shadow

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestDisabledPolicy(t *testing.T) {
	d := DisabledPolicy{}.Decide(context.Background(), provider.ChatRequest{})
	if d.Sample {
		t.Errorf("disabled policy should never sample")
	}
	if (DisabledPolicy{}).Name() != PolicyDisabled {
		t.Errorf("wrong name")
	}
}

func TestFixedPercentagePolicy_Sampling(t *testing.T) {
	// Deterministic sampler driven by a fixed sequence.
	seq := []float64{0.05, 0.40, 0.60, 0.99}
	i := 0
	sampler := func() float64 { v := seq[i%len(seq)]; i++; return v }

	p := NewFixedPercentagePolicy(50, sampler) // sample when r < 0.5
	want := []bool{true, true, false, false}
	for k, w := range want {
		if got := p.Decide(context.Background(), provider.ChatRequest{}); got.Sample != w {
			t.Errorf("sample %d (r=%.2f) = %v, want %v", k, seq[k], got.Sample, w)
		}
	}
}

func TestFixedPercentagePolicy_Boundaries(t *testing.T) {
	always := func() float64 { return 0.999 } // high value: only 100% samples it
	if NewFixedPercentagePolicy(0, always).Decide(context.Background(), provider.ChatRequest{}).Sample {
		t.Errorf("0%% should never sample")
	}
	if !NewFixedPercentagePolicy(100, always).Decide(context.Background(), provider.ChatRequest{}).Sample {
		t.Errorf("100%% should always sample")
	}
	d := NewFixedPercentagePolicy(25, always).Decide(context.Background(), provider.ChatRequest{})
	if d.Rate != 25 {
		t.Errorf("rate = %v, want 25", d.Rate)
	}
}

func TestPolicyRegistry_Build(t *testing.T) {
	reg := DefaultPolicyRegistry()
	if p, err := reg.Build(PolicyDisabled, DefaultConfig(), nil); err != nil || p.Name() != PolicyDisabled {
		t.Errorf("build disabled = (%v, %v)", p, err)
	}
	if p, err := reg.Build(PolicyFixedPercentage, Config{Percentage: 10}, nil); err != nil || p.Name() != PolicyFixedPercentage {
		t.Errorf("build fixed = (%v, %v)", p, err)
	}
	if _, err := reg.Build(PolicyRuleBased, DefaultConfig(), nil); !errors.Is(err, ErrPolicyNotImplemented) {
		t.Errorf("rule_based = %v, want ErrPolicyNotImplemented", err)
	}
	if _, err := reg.Build("bogus", DefaultConfig(), nil); !errors.Is(err, ErrUnknownPolicy) {
		t.Errorf("unknown = %v, want ErrUnknownPolicy", err)
	}
}

func TestConfig_Validate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("default config invalid: %v", err)
	}
	for _, pct := range []float64{-1, 101, 200} {
		if err := (Config{Policy: PolicyFixedPercentage, Percentage: pct}).Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("percentage %v should be invalid", pct)
		}
	}
	// Valid percentages from the spec.
	for _, pct := range []float64{1, 5, 10, 25, 50, 100} {
		if err := (Config{Policy: PolicyFixedPercentage, Percentage: pct}).Validate(); err != nil {
			t.Errorf("percentage %v should be valid: %v", pct, err)
		}
	}
}

func TestSelector_FirstOther(t *testing.T) {
	s := FirstOtherSelector{}
	candidates := []Target{{Provider: "openai", Model: "m"}, {Provider: "anthropic", Model: "m"}, {Provider: "cohere", Model: "m"}}

	got, ok := s.Select(Target{Provider: "openai"}, candidates)
	if !ok || got.Provider != "anthropic" {
		t.Errorf("select excluding openai = %q (ok=%v), want anthropic", got.Provider, ok)
	}
	// Only the primary is available → no secondary.
	if _, ok := s.Select(Target{Provider: "openai"}, []Target{{Provider: "openai"}}); ok {
		t.Errorf("single-provider pool should yield no secondary")
	}
}
