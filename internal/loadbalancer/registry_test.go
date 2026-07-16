package loadbalancer

import (
	"errors"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func inst(id, prov, region string) Instance {
	return Instance{ID: id, Provider: prov, Region: region}
}

func TestRegistry_RegisterValidation(t *testing.T) {
	r := NewInstanceRegistry(5, nil)

	if err := r.Register(Instance{Provider: "openai"}); !errors.Is(err, ErrInvalidInstance) {
		t.Errorf("register without ID = %v, want ErrInvalidInstance", err)
	}
	if err := r.Register(Instance{ID: "a"}); !errors.Is(err, ErrInvalidInstance) {
		t.Errorf("register without provider = %v, want ErrInvalidInstance", err)
	}
	if err := r.Register(inst("a", "openai", "us-east-1")); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(inst("a", "openai", "eu-west-1")); !errors.Is(err, ErrInstanceExists) {
		t.Errorf("duplicate register = %v, want ErrInstanceExists", err)
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1", r.Len())
	}
}

func TestRegistry_DeregisterEnableDisableUnknown(t *testing.T) {
	r := NewInstanceRegistry(5, nil)
	for _, m := range []func(string) error{r.Deregister, r.Enable, r.Disable} {
		if err := m("missing"); !errors.Is(err, ErrInstanceNotFound) {
			t.Errorf("op on missing = %v, want ErrInstanceNotFound", err)
		}
	}
	if err := r.SetHealth("missing", provider.HealthStateUnhealthy); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("SetHealth on missing = %v, want ErrInstanceNotFound", err)
	}
	if err := r.Update(Observation{InstanceID: "missing", Latency: time.Second}); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("Update on missing = %v, want ErrInstanceNotFound", err)
	}
}

func TestRegistry_DisablePreservesStats(t *testing.T) {
	r := NewInstanceRegistry(5, nil)
	_ = r.Register(inst("a", "openai", ""))
	r.markSelected("a")
	_ = r.Update(Observation{InstanceID: "a", Latency: 40 * time.Millisecond})
	if err := r.Disable("a"); err != nil {
		t.Fatal(err)
	}

	got := r.List()[0]
	if got.Enabled {
		t.Errorf("instance still enabled after Disable")
	}
	if got.RequestCount != 1 || got.AverageLatency != 40*time.Millisecond {
		t.Errorf("stats not preserved through Disable: count=%d latency=%v", got.RequestCount, got.AverageLatency)
	}
}

func TestRegistry_DiscoverReconciles(t *testing.T) {
	r := NewInstanceRegistry(5, nil)
	_ = r.Register(inst("a", "openai", "us-east-1"))
	r.markSelected("a") // give "a" some state to preserve

	// Discover: keep a (refreshed region), add b, drop nothing-else; c is new.
	err := r.Discover([]Instance{
		inst("a", "openai", "us-west-2"),
		inst("b", "anthropic", "us-east-1"),
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if r.Len() != 2 {
		t.Fatalf("Len after discover = %d, want 2", r.Len())
	}

	byID := map[string]InstanceStats{}
	for _, s := range r.List() {
		byID[s.ID] = s
	}
	if byID["a"].Region != "us-west-2" {
		t.Errorf("a region not refreshed: %q", byID["a"].Region)
	}
	if byID["a"].RequestCount != 1 {
		t.Errorf("a stats not preserved through discover: count=%d", byID["a"].RequestCount)
	}
	if _, ok := byID["b"]; !ok {
		t.Errorf("b not added by discover")
	}

	// Discovering a set without "a" drops it.
	_ = r.Discover([]Instance{inst("b", "anthropic", "us-east-1")})
	if r.Len() != 1 {
		t.Errorf("Len after drop = %d, want 1", r.Len())
	}
	if _, ok := r.descriptor("a"); ok {
		t.Errorf("a should have been dropped by discover")
	}
}
