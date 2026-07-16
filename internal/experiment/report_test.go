package experiment_test

import (
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/experiment"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func TestBuildReport_AssemblesAllSections(t *testing.T) {
	in := experiment.Inputs{
		Experiment:  "openai-vs-anthropic",
		Description: "cost/quality comparison",
		GeneratedAt: time.Unix(0, 0),
		Shadow:      shadow.Stats{Sampled: 20, Dispatched: 20},
		Evaluation: evaluation.Statistics{
			Records: 20, Comparable: 18,
			AvgCostDifference: -0.02, AvgLatencyDifference: -150 * time.Millisecond,
			AvgSimilarity: 0.91, ExactMatchRate: 0.4,
			ProviderWinRate: map[string]float64{"anthropic": 0.7, "openai": 0.2},
		},
		Classification: adaptive.Snapshot{
			Distribution: map[string]int{"simple": 12, "complex": 8}, AverageComplexity: 1.4,
		},
		ProviderUsage:    map[string]int{"openai": 60, "anthropic": 40},
		CacheSavingsUSD:  1.50,
		BudgetSavingsUSD: 0.80,
		MonthlyFactor:    100, // observed sample → monthly projection
	}

	r := experiment.BuildReport(in)

	if r.Comparable != 18 || r.ShadowSampled != 20 {
		t.Errorf("counts wrong: %+v", r)
	}
	if r.AvgSimilarity != 0.91 || r.ProviderWinRate["anthropic"] != 0.7 {
		t.Errorf("evaluation analytics wrong: %+v", r)
	}
	if r.ProviderUsage["openai"] != 60 || r.ClassificationDistribution["simple"] != 12 {
		t.Errorf("usage/classification wrong: %+v", r)
	}
	if r.CacheSavingsUSD != 1.5 || r.BudgetSavingsUSD != 0.8 {
		t.Errorf("savings wrong: %+v", r)
	}
	// (1.50 + 0.80) * 100 = 230
	if r.EstimatedMonthlySavingsUSD != 230 {
		t.Errorf("monthly savings = %v, want 230", r.EstimatedMonthlySavingsUSD)
	}
	// Similar + cheaper → promote recommendation naming the top provider.
	if !strings.Contains(r.Recommendation, "promoting") || !strings.Contains(r.Recommendation, "anthropic") {
		t.Errorf("recommendation = %q", r.Recommendation)
	}
}

func TestRecommendation_Cases(t *testing.T) {
	cases := []struct {
		name  string
		stats evaluation.Statistics
		want  string
	}{
		{"no data", evaluation.Statistics{}, "insufficient"},
		{"diverge", evaluation.Statistics{Comparable: 5, AvgSimilarity: 0.3}, "keep the primary"},
		{"similar+cheaper", evaluation.Statistics{Comparable: 5, AvgSimilarity: 0.9, AvgCostDifference: -0.01, ProviderWinRate: map[string]float64{"x": 0.8}}, "promoting"},
		{"similar but not cheaper", evaluation.Statistics{Comparable: 5, AvgSimilarity: 0.9, AvgCostDifference: 0.5}, "no decisive advantage"},
	}
	for _, tc := range cases {
		r := experiment.BuildReport(experiment.Inputs{Evaluation: tc.stats})
		if !strings.Contains(r.Recommendation, tc.want) {
			t.Errorf("%s: recommendation = %q, want to contain %q", tc.name, r.Recommendation, tc.want)
		}
	}
}

func TestBuildReport_MonthlyFactorDefaults(t *testing.T) {
	// No factor → monthly equals observed.
	r := experiment.BuildReport(experiment.Inputs{CacheSavingsUSD: 2, BudgetSavingsUSD: 3})
	if r.EstimatedMonthlySavingsUSD != 5 {
		t.Errorf("monthly with no factor = %v, want 5 (observed)", r.EstimatedMonthlySavingsUSD)
	}
}
