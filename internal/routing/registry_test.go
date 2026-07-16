package routing

import (
	"errors"
	"testing"
)

func TestDefaultRegistry_BuildsWeighted(t *testing.T) {
	s, err := DefaultRegistry().Build(StrategyWeighted, DefaultConfig())
	if err != nil {
		t.Fatalf("Build(weighted) = %v", err)
	}
	if s.Name() != StrategyWeighted {
		t.Errorf("built strategy = %q, want weighted", s.Name())
	}
}

func TestRegistry_UnknownStrategy(t *testing.T) {
	_, err := DefaultRegistry().Build("banana", DefaultConfig())
	if !errors.Is(err, ErrUnknownStrategy) {
		t.Fatalf("Build(banana) = %v, want ErrUnknownStrategy", err)
	}
}

func TestRegistry_ReservedStrategiesNotImplemented(t *testing.T) {
	reserved := []string{StrategyRoundRobin, StrategyRandom, StrategyCostFirst, StrategyLatencyFirst}
	for _, name := range reserved {
		_, err := DefaultRegistry().Build(name, DefaultConfig())
		if !errors.Is(err, ErrStrategyNotImplemented) {
			t.Errorf("Build(%q) = %v, want ErrStrategyNotImplemented", name, err)
		}
	}
}

func TestRegistry_RegisterAndDuplicate(t *testing.T) {
	r := NewRegistry()
	build := func(Config) (Strategy, error) { return NewWeighted(WeightedConfig{}), nil }

	if err := r.Register("custom", build); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	if !r.Supports("custom") {
		t.Errorf("Supports(custom) = false")
	}
	if err := r.Register("custom", build); err == nil {
		t.Errorf("duplicate Register() = nil, want error")
	}
	if err := r.Register("", build); err == nil {
		t.Errorf("Register(empty) = nil, want error")
	}
	if err := r.Register("x", nil); err == nil {
		t.Errorf("Register(nil builder) = nil, want error")
	}
}

func TestRegistry_Names(t *testing.T) {
	names := DefaultRegistry().Names()
	if len(names) != 1 || names[0] != StrategyWeighted {
		t.Errorf("Names() = %v, want [weighted]", names)
	}
}

func TestRegistry_BuilderErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	r := NewRegistry()
	_ = r.Register("boom", func(Config) (Strategy, error) { return nil, sentinel })
	if _, err := r.Build("boom", DefaultConfig()); !errors.Is(err, sentinel) {
		t.Errorf("Build() = %v, want wrap of sentinel", err)
	}
}
