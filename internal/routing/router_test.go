package routing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// fakeSource is a hand-rolled ProviderSource for isolated router tests.
type fakeSource struct {
	providers []string
	models    map[string][]provider.ModelInfo
	err       error
	// missing lists provider names that GetProvider should reject, to simulate a
	// validation failure without affecting enumeration.
	missing map[string]bool
}

func (f fakeSource) ListProviders() []string { return f.providers }

func (f fakeSource) ListModels(_ context.Context, name string) ([]provider.ModelInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.models[name], nil
}

func (f fakeSource) GetProvider(name string) (provider.LLMProvider, error) {
	if f.missing[name] {
		return nil, provider.NewError(name, "lookup", provider.ErrProviderNotFound)
	}
	for _, p := range f.providers {
		if p == name {
			return mock.New(mock.WithName(name)), nil
		}
	}
	return nil, provider.NewError(name, "lookup", provider.ErrProviderNotFound)
}

func chatModel(id string) provider.ModelInfo {
	return provider.ModelInfo{ID: id, Capabilities: []provider.Capability{provider.CapabilityChat}}
}
func embedModel(id string) provider.ModelInfo {
	return provider.ModelInfo{ID: id, Capabilities: []provider.Capability{provider.CapabilityEmbeddings}}
}

func newRouter(t *testing.T, src routing.ProviderSource, cfg routing.Config) routing.Router {
	t.Helper()
	r, err := routing.Build(src, cfg)
	if err != nil {
		t.Fatalf("routing.Build() = %v", err)
	}
	return r
}

func TestRoute_SelectsFirstCandidate(t *testing.T) {
	src := fakeSource{
		providers: []string{"anthropic", "openai"}, // sorted as Manager would return
		models: map[string][]provider.ModelInfo{
			"anthropic": {chatModel("claude")},
			"openai":    {chatModel("gpt-4o")},
		},
	}
	r := newRouter(t, src, routing.DefaultConfig())

	dec, err := r.Route(context.Background(), routing.RoutingContext{Capability: provider.CapabilityChat})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	if dec.Selected.Provider != "anthropic" || dec.Selected.Model != "claude" {
		t.Errorf("Selected = %+v, want anthropic/claude (first candidate)", dec.Selected)
	}
	if len(dec.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2", len(dec.Candidates))
	}
	if dec.Strategy != routing.StrategyWeighted {
		t.Errorf("strategy = %q", dec.Strategy)
	}
	// Explanation is populated and marks exactly the selected candidate.
	if dec.Explanation.Considered != 2 || !dec.Explanation.Candidates[0].Selected {
		t.Errorf("explanation wrong: %+v", dec.Explanation)
	}
	if dec.Explanation.Candidates[1].Selected {
		t.Errorf("only the first candidate should be marked selected")
	}
}

func TestRoute_NoCandidates(t *testing.T) {
	r := newRouter(t, fakeSource{}, routing.DefaultConfig())
	_, err := r.Route(context.Background(), routing.RoutingContext{})
	if !errors.Is(err, routing.ErrNoCandidates) {
		t.Fatalf("Route(empty) = %v, want ErrNoCandidates", err)
	}
}

func TestRoute_CapabilityFilter(t *testing.T) {
	src := fakeSource{
		providers: []string{"p"},
		models:    map[string][]provider.ModelInfo{"p": {chatModel("c"), embedModel("e")}},
	}
	r := newRouter(t, src, routing.DefaultConfig())

	// Embeddings capability must select only the embedding model.
	dec, err := r.Route(context.Background(), routing.RoutingContext{Capability: provider.CapabilityEmbeddings})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	if len(dec.Candidates) != 1 || dec.Selected.Model != "e" {
		t.Errorf("embeddings routing = %+v, want only model 'e'", dec.Candidates)
	}
}

func TestRoute_ModelPreference(t *testing.T) {
	src := fakeSource{
		providers: []string{"p"},
		models:    map[string][]provider.ModelInfo{"p": {chatModel("a"), chatModel("b")}},
	}
	r := newRouter(t, src, routing.DefaultConfig())

	dec, err := r.Route(context.Background(), routing.RoutingContext{
		Capability: provider.CapabilityChat, Model: "b",
	})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	if len(dec.Candidates) != 1 || dec.Selected.Model != "b" {
		t.Errorf("model preference not honored: %+v", dec.Candidates)
	}
}

func TestRoute_ProviderConstraint(t *testing.T) {
	src := fakeSource{
		providers: []string{"a", "b"},
		models:    map[string][]provider.ModelInfo{"a": {chatModel("x")}, "b": {chatModel("y")}},
	}
	r := newRouter(t, src, routing.DefaultConfig())

	dec, err := r.Route(context.Background(), routing.RoutingContext{
		Constraints: routing.Constraints{Providers: []string{"b"}},
	})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	if dec.Selected.Provider != "b" || len(dec.Candidates) != 1 {
		t.Errorf("provider constraint not honored: %+v", dec.Candidates)
	}
}

func TestRoute_ModelConstraint(t *testing.T) {
	src := fakeSource{
		providers: []string{"a"},
		models:    map[string][]provider.ModelInfo{"a": {chatModel("x"), chatModel("y")}},
	}
	r := newRouter(t, src, routing.DefaultConfig())

	dec, err := r.Route(context.Background(), routing.RoutingContext{
		Constraints: routing.Constraints{Models: []string{"y"}},
	})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	if len(dec.Candidates) != 1 || dec.Selected.Model != "y" {
		t.Errorf("model constraint not honored: %+v", dec.Candidates)
	}
}

func TestRoute_SkipsProviderWithDiscoveryError(t *testing.T) {
	// A source that errors on discovery yields no candidates rather than crashing.
	src := fakeSource{providers: []string{"a"}, err: errors.New("boom")}
	r := newRouter(t, src, routing.DefaultConfig())
	if _, err := r.Route(context.Background(), routing.RoutingContext{}); !errors.Is(err, routing.ErrNoCandidates) {
		t.Errorf("Route() = %v, want ErrNoCandidates when discovery fails", err)
	}
}

func TestBuild_ReservedStrategyFailsFast(t *testing.T) {
	_, err := routing.Build(fakeSource{}, routing.Config{Strategy: routing.StrategyRoundRobin})
	if !errors.Is(err, routing.ErrStrategyNotImplemented) {
		t.Fatalf("Build(round_robin) = %v, want ErrStrategyNotImplemented", err)
	}
}

func TestBuild_InvalidConfigFailsFast(t *testing.T) {
	_, err := routing.Build(fakeSource{}, routing.Config{
		Strategy: "weighted",
		Weighted: routing.WeightedConfig{DefaultWeight: -5},
	})
	if !errors.Is(err, routing.ErrInvalidRoutingConfig) {
		t.Fatalf("Build(invalid) = %v, want ErrInvalidRoutingConfig", err)
	}
}

func TestRoute_ExplanationIsComplete(t *testing.T) {
	// "gpt" is both cheaper and higher quality than "claude" here, so it wins on
	// score (not merely tie-break), and the explanation must reflect that.
	src := fakeSource{
		providers: []string{"anthropic", "openai"},
		models: map[string][]provider.ModelInfo{
			"openai":    {chatModel("gpt")},
			"anthropic": {chatModel("claude")},
		},
	}
	cfg := routing.DefaultConfig()
	cfg.Weighted.Factors = routing.FactorWeights{Cost: 1, Quality: 1}
	cfg.Weighted.Cost = routing.CostConfig{
		Pricing: map[string]routing.ModelPricing{
			"gpt":    {InputPer1K: 0.001},
			"claude": {InputPer1K: 0.010},
		},
		EstimatedInputTokens: 1000,
	}
	cfg.Weighted.Quality = routing.QualityConfig{Models: map[string]float64{"gpt": 0.95, "claude": 0.90}}

	r := newRouter(t, src, cfg)
	dec, err := r.Route(context.Background(), routing.RoutingContext{Capability: provider.CapabilityChat})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}

	if dec.Selected.Model != "gpt" {
		t.Fatalf("winner = %q, want gpt (cheaper + higher quality)", dec.Selected.Model)
	}

	ex := dec.Explanation
	// Machine-readable: normalized weights present and summing to ~1.
	total := 0.0
	for _, w := range ex.Weights {
		total += w
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("explanation weights sum = %v, want ~1", total)
	}
	// Per-candidate breakdown present, ranked, exactly one selected.
	if len(ex.Candidates) != 2 {
		t.Fatalf("explanation candidates = %d, want 2", len(ex.Candidates))
	}
	top := ex.Candidates[0]
	if !top.Selected || top.Rank != 1 || top.Provider != "openai" {
		t.Errorf("top candidate wrong: %+v", top)
	}
	if len(top.Factors) != 4 {
		t.Errorf("top candidate factors = %v, want 4 factors", top.Factors)
	}
	if ex.Candidates[1].Selected || ex.Candidates[1].Rank != 2 {
		t.Errorf("runner-up wrong: %+v", ex.Candidates[1])
	}
	// Human-readable: the top-level reason explains the win.
	if ex.Reason == "" || !contains(ex.Reason, "won") {
		t.Errorf("explanation reason not human-readable: %q", ex.Reason)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOfSub(s, sub) >= 0)
}

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestRoute_IntegratesWithProviderManager exercises the router against a REAL
// provider.Manager (backed by mock providers), proving the ProviderSource seam
// integrates with the completed Provider Layer.
func TestRoute_IntegratesWithProviderManager(t *testing.T) {
	reg := provider.NewRegistry()
	_ = reg.Register(mock.New(mock.WithName("openai")))
	_ = reg.Register(mock.New(mock.WithName("anthropic")))
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	// *provider.Manager satisfies routing.ProviderSource directly.
	var src routing.ProviderSource = pm
	r := newRouter(t, src, routing.DefaultConfig())

	dec, err := r.Route(context.Background(), routing.RoutingContext{Capability: provider.CapabilityChat})
	if err != nil {
		t.Fatalf("Route() = %v", err)
	}
	// The mock advertises a chat model ("mock-chat"); both providers offer it.
	if dec.Selected.Model != "mock-chat" {
		t.Errorf("selected model = %q, want mock-chat", dec.Selected.Model)
	}
	if dec.Selected.Provider != "anthropic" {
		t.Errorf("selected provider = %q, want anthropic (first sorted)", dec.Selected.Provider)
	}

	// Convenience context helper round-trips the requested model.
	rc := routing.ChatContext(provider.ChatRequest{Model: "mock-chat"})
	if rc.Capability != provider.CapabilityChat || rc.Model != "mock-chat" {
		t.Errorf("ChatContext = %+v", rc)
	}
}
