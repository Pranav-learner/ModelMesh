package routing_test

// End-to-end integration tests for the finalized Routing Engine: the full
// pipeline from provider discovery through scoring, ranking, validation,
// fallback, decision logging, and metrics. No external APIs are used.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// realRouter builds a routing.Manager over a REAL provider.Manager backed by mock
// providers, proving integration with the completed Provider Layer.
func realRouter(t *testing.T, cfg routing.Config, opts []routing.Option, names ...string) (*routing.Manager, *provider.Manager) {
	t.Helper()
	reg := provider.NewRegistry()
	for _, n := range names {
		if err := reg.Register(mock.New(mock.WithName(n))); err != nil {
			t.Fatalf("register %s: %v", n, err)
		}
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider(names[0]))
	r, err := routing.Build(pm, cfg, opts...)
	if err != nil {
		t.Fatalf("routing.Build() = %v", err)
	}
	return r, pm
}

func chatRC() routing.RoutingContext {
	return routing.RoutingContext{Capability: provider.CapabilityChat, RequestID: "req-1", Summary: "summarize this"}
}

func TestIntegration_SingleProvider(t *testing.T) {
	r, _ := realRouter(t, routing.DefaultConfig(), nil, "openai")

	sel, err := r.Select(context.Background(), chatRC())
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}
	if sel.Selected.Provider != "openai" || sel.FallbackUsed {
		t.Errorf("selection = %+v, want openai without fallback", sel.Selected)
	}
	if sel.Provider == nil || sel.Provider.Name() != "openai" {
		t.Errorf("resolved provider wrong: %v", sel.Provider)
	}
}

func TestIntegration_MultipleProviders_RankAndSelect(t *testing.T) {
	// Distinguish two providers by per-provider quality so scoring (not tie-break)
	// decides the winner. Both mocks offer "mock-chat".
	cfg := routing.DefaultConfig()
	cfg.Weighted.Factors = routing.FactorWeights{Quality: 1}
	cfg.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{"openai": 0.95, "anthropic": 0.85}}

	r, _ := realRouter(t, cfg, nil, "openai", "anthropic")

	sel, err := r.Select(context.Background(), chatRC())
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}
	if sel.Selected.Provider != "openai" {
		t.Errorf("winner = %q, want openai (higher quality)", sel.Selected.Provider)
	}
	// Ranking: openai first, anthropic second.
	ranks := sel.Decision.Candidates
	if ranks[0].Provider != "openai" || ranks[1].Provider != "anthropic" {
		t.Errorf("ranking = %v/%v, want openai/anthropic", ranks[0].Provider, ranks[1].Provider)
	}
}

func TestIntegration_DifferentWeights_ChangeWinner(t *testing.T) {
	// Quality-dominant weighting favors openai; latency-dominant weighting (with
	// anthropic faster) flips the winner — the same providers, different weights.
	cfg1 := routing.DefaultConfig()
	cfg1.Weighted.Factors = routing.FactorWeights{Quality: 1}
	cfg1.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{"openai": 0.99, "anthropic": 0.80}}
	r1, _ := realRouter(t, cfg1, nil, "openai", "anthropic")
	sel1, _ := r1.Select(context.Background(), chatRC())
	if sel1.Selected.Provider != "openai" {
		t.Errorf("quality-dominant winner = %q, want openai", sel1.Selected.Provider)
	}

	cfg2 := routing.DefaultConfig()
	cfg2.Weighted.Factors = routing.FactorWeights{Latency: 1}
	cfg2.Weighted.Latency = routing.LatencyConfig{Providers: map[string]time.Duration{"openai": 900 * time.Millisecond, "anthropic": 100 * time.Millisecond}}
	r2, _ := realRouter(t, cfg2, nil, "openai", "anthropic")
	sel2, _ := r2.Select(context.Background(), chatRC())
	if sel2.Selected.Provider != "anthropic" {
		t.Errorf("latency-dominant winner = %q, want anthropic (faster)", sel2.Selected.Provider)
	}
}

func TestIntegration_FallbackOnValidationFailure(t *testing.T) {
	// "a" ranks first (name tie-break) but is not resolvable -> fall back to "b".
	src := fakeSource{
		providers: []string{"a", "b"},
		models:    map[string][]provider.ModelInfo{"a": {chatModel("m")}, "b": {chatModel("m")}},
		missing:   map[string]bool{"a": true},
	}
	r, err := routing.Build(src, routing.DefaultConfig())
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	sel, err := r.Select(context.Background(), chatRC())
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}
	if sel.Selected.Provider != "b" || !sel.FallbackUsed || sel.Attempts != 2 {
		t.Errorf("fallback selection = %+v, want b, fallback=true, attempts=2", sel)
	}
}

func TestIntegration_AllCandidatesInvalid(t *testing.T) {
	src := fakeSource{
		providers: []string{"a", "b"},
		models:    map[string][]provider.ModelInfo{"a": {chatModel("m")}, "b": {chatModel("m")}},
		missing:   map[string]bool{"a": true, "b": true},
	}
	r, _ := routing.Build(src, routing.DefaultConfig())

	_, err := r.Select(context.Background(), chatRC())
	if !errors.Is(err, routing.ErrNoValidProvider) {
		t.Fatalf("Select() = %v, want ErrNoValidProvider", err)
	}
}

func TestIntegration_UnsupportedModel(t *testing.T) {
	r, _ := realRouter(t, routing.DefaultConfig(), nil, "openai")

	_, err := r.Select(context.Background(), routing.RoutingContext{
		Capability: provider.CapabilityChat, Model: "no-such-model",
	})
	if !errors.Is(err, routing.ErrNoCandidates) {
		t.Fatalf("Select(bad model) = %v, want ErrNoCandidates", err)
	}
}

func TestIntegration_MissingProvider_NoCandidates(t *testing.T) {
	// Empty provider set -> no candidates.
	src := fakeSource{}
	r, _ := routing.Build(src, routing.DefaultConfig())
	if _, err := r.Select(context.Background(), chatRC()); !errors.Is(err, routing.ErrNoCandidates) {
		t.Errorf("Select(empty) = %v, want ErrNoCandidates", err)
	}
}

func TestIntegration_TieBreakingDeterministic(t *testing.T) {
	r, _ := realRouter(t, routing.DefaultConfig(), nil, "zeta", "alpha")
	// Both identical mocks -> tie -> deterministic by name -> alpha.
	for i := 0; i < 10; i++ {
		sel, err := r.Select(context.Background(), chatRC())
		if err != nil {
			t.Fatalf("Select() = %v", err)
		}
		if sel.Selected.Provider != "alpha" {
			t.Fatalf("tie-break winner = %q, want alpha (deterministic)", sel.Selected.Provider)
		}
	}
}

func TestIntegration_Explanation(t *testing.T) {
	cfg := routing.DefaultConfig()
	cfg.Weighted.Factors = routing.FactorWeights{Quality: 1}
	cfg.Weighted.Quality = routing.QualityConfig{Providers: map[string]float64{"openai": 0.95, "anthropic": 0.85}}
	r, _ := realRouter(t, cfg, nil, "openai", "anthropic")

	sel, err := r.Select(context.Background(), chatRC())
	if err != nil {
		t.Fatalf("Select() = %v", err)
	}

	text := routing.Explain(sel.Decision)
	for _, want := range []string{"Routing decision", "Winner:", "openai", "Reason:"} {
		if !strings.Contains(text, want) {
			t.Errorf("Explain() missing %q:\n%s", want, text)
		}
	}
	if len(routing.Rankings(sel.Decision)) != 2 {
		t.Errorf("Rankings() len = %d, want 2", len(routing.Rankings(sel.Decision)))
	}
	if !strings.Contains(routing.InspectSelection(sel), "openai") {
		t.Errorf("InspectSelection missing provider: %s", routing.InspectSelection(sel))
	}
}

func TestIntegration_MetricsCollection(t *testing.T) {
	metrics := routing.NewMetricsCollector()

	// Real router: one successful selection.
	r, _ := realRouter(t, routing.DefaultConfig(), []routing.Option{routing.WithMetrics(metrics)}, "openai", "anthropic")
	if _, err := r.Select(context.Background(), chatRC()); err != nil {
		t.Fatalf("Select() = %v", err)
	}

	// Fake source: one fallback and one total failure, sharing the collector.
	fb := fakeSource{providers: []string{"a", "b"}, models: map[string][]provider.ModelInfo{"a": {chatModel("m")}, "b": {chatModel("m")}}, missing: map[string]bool{"a": true}}
	rf, _ := routing.Build(fb, routing.DefaultConfig(), routing.WithMetrics(metrics))
	if _, err := rf.Select(context.Background(), chatRC()); err != nil {
		t.Fatalf("Select(fallback) = %v", err)
	}

	fail := fakeSource{providers: []string{"a"}, models: map[string][]provider.ModelInfo{"a": {chatModel("m")}}, missing: map[string]bool{"a": true}}
	rfail, _ := routing.Build(fail, routing.DefaultConfig(), routing.WithMetrics(metrics))
	_, _ = rfail.Select(context.Background(), chatRC())

	s := metrics.Snapshot()
	if s.TotalDecisions != 3 {
		t.Errorf("TotalDecisions = %d, want 3", s.TotalDecisions)
	}
	if s.FallbackCount != 1 {
		t.Errorf("FallbackCount = %d, want 1", s.FallbackCount)
	}
	if s.FailedAttempts != 1 {
		t.Errorf("FailedAttempts = %d, want 1", s.FailedAttempts)
	}
}

func TestIntegration_DecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf, logger.LevelInfo)
	r, _ := realRouter(t, routing.DefaultConfig(), []routing.Option{routing.WithLogger(log)}, "openai")

	if _, err := r.Select(context.Background(), chatRC()); err != nil {
		t.Fatalf("Select() = %v", err)
	}

	out := buf.String()
	for _, field := range []string{
		"routing decision", "request_id", "req-1", "prompt_summary",
		"selected_provider", "openai", "routing_duration", "fallback_used",
		"candidate_scores",
	} {
		if !strings.Contains(out, field) {
			t.Errorf("decision log missing %q:\n%s", field, out)
		}
	}
}
