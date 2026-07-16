package evaluation_test

import (
	"context"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func comparison(corr, pModel, sModel, pText, sText string, tokens int, pLat, sLat time.Duration) shadow.Comparison {
	return shadow.Comparison{
		CorrelationID:   corr,
		Primary:         shadow.Target{Provider: "openai", Model: pModel},
		Shadow:          shadow.Target{Provider: "anthropic", Model: sModel},
		PrimaryResponse: resp("openai", pModel, pText, tokens),
		PrimaryLatency:  pLat,
		ShadowResult:    shadow.ShadowResult{Success: true, Response: resp("anthropic", sModel, sText, tokens), Latency: sLat},
	}
}

func TestStatistics(t *testing.T) {
	e := evaluation.New(evaluation.WithCostModel(perModelCost()))
	ctx := context.Background()

	// Record 1: shadow cheaper (1 vs 10) + faster (200 vs 800) → shadow wins; identical text.
	e.Evaluate(ctx, comparison("r1", "expensive", "cheap", "hello", "hello", 1000, 800*time.Millisecond, 200*time.Millisecond))
	// Record 2: shadow cheaper but slower (500 vs 300) → tie; identical text.
	e.Evaluate(ctx, comparison("r2", "expensive", "cheap", "hello", "hello", 1000, 300*time.Millisecond, 500*time.Millisecond))
	// A failed shadow: recorded but not comparable, excluded from averages.
	e.Evaluate(ctx, shadow.Comparison{
		CorrelationID: "r3", Primary: shadow.Target{Provider: "openai", Model: "expensive"},
		Shadow: shadow.Target{Provider: "anthropic", Model: "cheap"}, ShadowResult: shadow.ShadowResult{Success: false, Err: "boom"},
	})

	s := e.Statistics()
	if s.Records != 3 || s.Comparable != 2 {
		t.Fatalf("records=%d comparable=%d, want 3/2", s.Records, s.Comparable)
	}
	// Latency diffs: r1 = 200-800 = -600ms; r2 = 500-300 = +200ms → avg -200ms.
	if s.AvgLatencyDifference != -200*time.Millisecond {
		t.Errorf("avg latency diff = %v, want -200ms", s.AvgLatencyDifference)
	}
	// Cost diffs: both shadow cheaper by 9 → avg -9.
	if s.AvgCostDifference != -9 {
		t.Errorf("avg cost diff = %v, want -9", s.AvgCostDifference)
	}
	if s.AvgTokenDifference != 0 {
		t.Errorf("avg token diff = %v, want 0", s.AvgTokenDifference)
	}
	if s.AvgSimilarity != 1 || s.ExactMatchRate != 1 {
		t.Errorf("avg similarity = %v, exact rate = %v, want 1/1", s.AvgSimilarity, s.ExactMatchRate)
	}
	// Win rate: anthropic won r1, tie in r2 → 1 win over 2 appearances = 0.5; openai 0/2.
	if s.ProviderWinRate["anthropic"] != 0.5 {
		t.Errorf("anthropic win rate = %v, want 0.5", s.ProviderWinRate["anthropic"])
	}
	if s.ProviderWinRate["openai"] != 0 {
		t.Errorf("openai win rate = %v, want 0", s.ProviderWinRate["openai"])
	}
}

func TestStatistics_Empty(t *testing.T) {
	s := evaluation.New().Statistics()
	if s.Records != 0 || s.Comparable != 0 || s.AvgSimilarity != 0 {
		t.Errorf("empty statistics should be zero-valued: %+v", s)
	}
}
