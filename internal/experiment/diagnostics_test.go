package experiment_test

import (
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/experiment"
	"github.com/symbiotes/modelmesh/internal/routing"
)

func TestExplainExperiment(t *testing.T) {
	m := experiment.NewManager()
	eval := evaluation.New()
	e, _ := m.Create("compare", "openai vs anthropic", eval,
		experiment.WithCacheSavings(func() float64 { return 1.0 }))
	feed(eval, "r1", "gpt-4", "claude", 100, 500*time.Millisecond, 300*time.Millisecond)

	out := experiment.ExplainExperiment(e)
	for _, want := range []string{"compare", "openai vs anthropic", "evaluations", "recommendation", "savings"} {
		if !strings.Contains(out, want) {
			t.Errorf("ExplainExperiment missing %q:\n%s", want, out)
		}
	}
}

func TestInspectComparison(t *testing.T) {
	eval := evaluation.New()
	feed(eval, "req-x", "expensive", "cheap", 200, 600*time.Millisecond, 200*time.Millisecond)
	recs := eval.Records()
	if len(recs) != 1 {
		t.Fatal("expected one record")
	}
	out := experiment.InspectComparison(recs[0])
	for _, want := range []string{"req-x", "primary:", "shadow:", "quality:", "latency:", "cost:", "winner:"} {
		if !strings.Contains(out, want) {
			t.Errorf("InspectComparison missing %q:\n%s", want, out)
		}
	}
}

func TestInspectComparison_Failed(t *testing.T) {
	eval := evaluation.New()
	// A failed shadow record.
	feedFailed(eval, "req-fail", "boom")
	out := experiment.InspectComparison(eval.Records()[0])
	if !strings.Contains(out, "shadow failed") || !strings.Contains(out, "boom") {
		t.Errorf("failed comparison should show the error: %s", out)
	}
}

func TestEvaluationHistory(t *testing.T) {
	eval := evaluation.New()
	for i := 0; i < 5; i++ {
		feed(eval, "r", "gpt-4", "claude", 100, 500*time.Millisecond, 400*time.Millisecond)
	}
	out := experiment.EvaluationHistory(eval.Records(), 3)
	if !strings.Contains(out, "CORRELATION") || !strings.Contains(out, "WINNER") {
		t.Errorf("history should render a header:\n%s", out)
	}
	// Limit to 3 → 3 data rows + header + separator.
	if lines := strings.Count(out, "\n"); lines < 4 {
		t.Errorf("expected at least header + 3 rows, got %d lines", lines)
	}
}

func TestShowRoutingDecision(t *testing.T) {
	d := routing.RoutingDecision{
		Strategy: "weighted",
		Selected: routing.Candidate{Provider: "openai", Model: "gpt-4o-mini", Score: 0.9, Reason: "won on cost"},
		Candidates: []routing.Candidate{
			{Provider: "openai", Model: "gpt-4o-mini", Score: 0.9},
			{Provider: "anthropic", Model: "claude", Score: 0.7},
		},
	}
	out := experiment.ShowRoutingDecision(d)
	if !strings.Contains(out, "openai/gpt-4o-mini") || !strings.Contains(out, "won on cost") {
		t.Errorf("ShowRoutingDecision missing detail:\n%s", out)
	}
}
