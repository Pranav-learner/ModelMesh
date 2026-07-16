package factory_test

import (
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/config"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/factory"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
)

func mockBuilder(pc config.ProviderConfig, _ factory.Deps) (provider.LLMProvider, error) {
	return mock.New(mock.WithName(pc.Name)), nil
}

func TestFactory_RegisterAndBuild(t *testing.T) {
	f := factory.New(factory.Deps{})
	if err := f.Register("mock", mockBuilder); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	p, err := f.Build(config.ProviderConfig{Name: "mock", Enabled: true})
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}
	if p.Name() != "mock" {
		t.Errorf("built provider name = %q, want mock", p.Name())
	}
}

func TestFactory_RegisterInvalid(t *testing.T) {
	f := factory.New(factory.Deps{})
	if err := f.Register("", mockBuilder); err == nil {
		t.Errorf("Register(empty name) = nil, want error")
	}
	if err := f.Register("x", nil); err == nil {
		t.Errorf("Register(nil builder) = nil, want error")
	}
}

func TestFactory_RegisterDuplicate(t *testing.T) {
	f := factory.New(factory.Deps{})
	_ = f.Register("mock", mockBuilder)
	if err := f.Register("mock", mockBuilder); err == nil {
		t.Errorf("duplicate Register() = nil, want error")
	}
}

func TestFactory_BuildUnknown(t *testing.T) {
	f := factory.New(factory.Deps{})
	_, err := f.Build(config.ProviderConfig{Name: "ghost"})
	if err == nil {
		t.Fatalf("Build(unknown) = nil, want error")
	}
}

func TestFactory_BuilderErrorPropagates(t *testing.T) {
	sentinel := errors.New("bad config")
	f := factory.New(factory.Deps{})
	_ = f.Register("boom", func(config.ProviderConfig, factory.Deps) (provider.LLMProvider, error) {
		return nil, sentinel
	})

	_, err := f.Build(config.ProviderConfig{Name: "boom"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Build() = %v, want wrap of sentinel", err)
	}
}

func TestFactory_NilBuilderResult(t *testing.T) {
	f := factory.New(factory.Deps{})
	_ = f.Register("nilp", func(config.ProviderConfig, factory.Deps) (provider.LLMProvider, error) {
		return nil, nil
	})
	if _, err := f.Build(config.ProviderConfig{Name: "nilp"}); err == nil {
		t.Errorf("Build(nil result) = nil, want error")
	}
}

func TestFactory_BuildAll_SkipsDisabled(t *testing.T) {
	f := factory.New(factory.Deps{})
	_ = f.Register("a", mockBuilder)
	_ = f.Register("b", mockBuilder)

	cfg := config.Config{Providers: []config.ProviderConfig{
		{Name: "a", Enabled: true},
		{Name: "b", Enabled: false},
	}}

	built, err := f.BuildAll(cfg)
	if err != nil {
		t.Fatalf("BuildAll() = %v", err)
	}
	if len(built) != 1 || built[0].Name() != "a" {
		t.Errorf("BuildAll built %d providers, want only 'a'", len(built))
	}
}

func TestFactory_SupportsAndNames(t *testing.T) {
	f := factory.New(factory.Deps{})
	_ = f.Register("b", mockBuilder)
	_ = f.Register("a", mockBuilder)

	if !f.Supports("a") || f.Supports("z") {
		t.Errorf("Supports wrong")
	}
	names := f.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("Names() = %v, want sorted [a b]", names)
	}
}
