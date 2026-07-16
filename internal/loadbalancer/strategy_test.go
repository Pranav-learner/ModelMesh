package loadbalancer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func cand(id string, latency time.Duration, samples int, requests uint64) Candidate {
	return Candidate{
		Instance: Instance{ID: id, Provider: "openai"},
		Stats:    InstanceStats{ID: id, Provider: "openai", AverageLatency: latency, Samples: samples, RequestCount: requests},
	}
}

func TestRoundRobin_Rotates(t *testing.T) {
	rr := NewRoundRobin()
	cands := []Candidate{cand("a", 0, 0, 0), cand("b", 0, 0, 0), cand("c", 0, 0, 0)}

	var seq []string
	for i := 0; i < 7; i++ {
		c, err := rr.Pick(context.Background(), Request{}, cands)
		if err != nil {
			t.Fatal(err)
		}
		seq = append(seq, c.Instance.ID)
	}
	want := []string{"a", "b", "c", "a", "b", "c", "a"}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("round robin sequence = %v, want %v", seq, want)
		}
	}
}

func TestRoundRobin_Empty(t *testing.T) {
	if _, err := NewRoundRobin().Pick(context.Background(), Request{}, nil); !errors.Is(err, ErrNoInstances) {
		t.Errorf("empty pick = %v, want ErrNoInstances", err)
	}
}

func TestLeastLatency_PicksFastest(t *testing.T) {
	ll := NewLeastLatency()
	cands := []Candidate{
		cand("a", 100*time.Millisecond, 5, 5),
		cand("b", 10*time.Millisecond, 5, 5),
		cand("c", 50*time.Millisecond, 5, 5),
	}
	c, err := ll.Pick(context.Background(), Request{}, cands)
	if err != nil {
		t.Fatal(err)
	}
	if c.Instance.ID != "b" {
		t.Errorf("least latency picked %q, want b", c.Instance.ID)
	}
}

func TestLeastLatency_ExploresUnmeasuredFirst(t *testing.T) {
	ll := NewLeastLatency()
	cands := []Candidate{
		cand("a", 10*time.Millisecond, 5, 5), // fast but measured
		cand("b", 0, 0, 0),                   // unmeasured
	}
	c, _ := ll.Pick(context.Background(), Request{}, cands)
	if c.Instance.ID != "b" {
		t.Errorf("expected unmeasured instance b to be explored first, got %q", c.Instance.ID)
	}
}

func TestLeastLatency_TieBreak(t *testing.T) {
	ll := NewLeastLatency()
	// Equal latency + equal samples: fewer requests wins, then ID.
	cands := []Candidate{
		cand("b", 20*time.Millisecond, 3, 10),
		cand("a", 20*time.Millisecond, 3, 10),
	}
	c, _ := ll.Pick(context.Background(), Request{}, cands)
	if c.Instance.ID != "a" {
		t.Errorf("tie-break picked %q, want a (lower ID)", c.Instance.ID)
	}
}

func TestStrategyRegistry_Build(t *testing.T) {
	reg := DefaultRegistry()

	if s, err := reg.Build(StrategyRoundRobin, DefaultConfig()); err != nil || s.Name() != StrategyRoundRobin {
		t.Errorf("build round_robin = (%v, %v)", s, err)
	}
	if s, err := reg.Build(StrategyLeastLatency, DefaultConfig()); err != nil || s.Name() != StrategyLeastLatency {
		t.Errorf("build least_latency = (%v, %v)", s, err)
	}
	if _, err := reg.Build(StrategyConsistentHashing, DefaultConfig()); !errors.Is(err, ErrStrategyNotImplemented) {
		t.Errorf("reserved strategy = %v, want ErrStrategyNotImplemented", err)
	}
	if _, err := reg.Build("nonsense", DefaultConfig()); !errors.Is(err, ErrUnknownStrategy) {
		t.Errorf("unknown strategy = %v, want ErrUnknownStrategy", err)
	}
}
