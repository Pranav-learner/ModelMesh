package experiment

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// Report is the analytics summary of an experiment: comparison outcomes, provider
// usage, classification distribution, savings, and a deterministic recommendation.
// It is serializable (json tags) so Part-3 report sinks and dashboards consume it
// directly.
type Report struct {
	Experiment  string    `json:"experiment"`
	Description string    `json:"description,omitempty"`
	GeneratedAt time.Time `json:"generated_at"`

	// Shadow + evaluation.
	ShadowSampled int `json:"shadow_sampled"`
	Evaluations   int `json:"evaluations"`
	Comparable    int `json:"comparable"`

	ProviderWinRate      map[string]float64 `json:"provider_win_rate"`
	AvgCostDifference    float64            `json:"avg_cost_difference"`
	AvgLatencyDifference time.Duration      `json:"avg_latency_difference"`
	AvgSimilarity        float64            `json:"avg_similarity"`
	ExactMatchRate       float64            `json:"exact_match_rate"`

	// Usage + classification.
	ProviderUsage              map[string]int `json:"provider_usage,omitempty"`
	ClassificationDistribution map[string]int `json:"classification_distribution,omitempty"`
	AvgComplexity              float64        `json:"avg_complexity"`

	// Savings.
	CacheSavingsUSD            float64 `json:"cache_savings_usd"`
	BudgetSavingsUSD           float64 `json:"budget_savings_usd"`
	EstimatedMonthlySavingsUSD float64 `json:"estimated_monthly_savings_usd"`

	Recommendation string `json:"recommendation"`
}

// Inputs is the immutable snapshot BuildReport assembles a Report from. Each
// telemetry source is optional (zero values are safe).
type Inputs struct {
	Experiment  string
	Description string
	GeneratedAt time.Time

	Shadow         shadow.Stats
	Evaluation     evaluation.Statistics
	Classification adaptive.Snapshot
	ProviderUsage  map[string]int

	CacheSavingsUSD  float64
	BudgetSavingsUSD float64
	// MonthlyFactor projects the observed savings to a monthly estimate (e.g. the
	// ratio of monthly request volume to the observed sample). 0 disables the
	// projection (monthly = observed).
	MonthlyFactor float64
}

// BuildReport assembles a Report from an Inputs snapshot. It is pure and
// deterministic.
func BuildReport(in Inputs) Report {
	factor := in.MonthlyFactor
	if factor <= 0 {
		factor = 1
	}
	observedSavings := in.CacheSavingsUSD + in.BudgetSavingsUSD

	r := Report{
		Experiment:  in.Experiment,
		Description: in.Description,
		GeneratedAt: in.GeneratedAt,

		ShadowSampled: int(in.Shadow.Sampled),
		Evaluations:   in.Evaluation.Records,
		Comparable:    in.Evaluation.Comparable,

		ProviderWinRate:      copyFloatMap(in.Evaluation.ProviderWinRate),
		AvgCostDifference:    in.Evaluation.AvgCostDifference,
		AvgLatencyDifference: in.Evaluation.AvgLatencyDifference,
		AvgSimilarity:        in.Evaluation.AvgSimilarity,
		ExactMatchRate:       in.Evaluation.ExactMatchRate,

		ProviderUsage:              copyIntMap(in.ProviderUsage),
		ClassificationDistribution: copyIntMap(in.Classification.Distribution),
		AvgComplexity:              in.Classification.AverageComplexity,

		CacheSavingsUSD:            round4(in.CacheSavingsUSD),
		BudgetSavingsUSD:           round4(in.BudgetSavingsUSD),
		EstimatedMonthlySavingsUSD: round4(observedSavings * factor),
	}
	r.Recommendation = recommend(in.Evaluation)
	return r
}

// recommend produces a deterministic promotion recommendation from the evaluation
// statistics. It never fabricates confidence: with no comparable data it says so.
func recommend(s evaluation.Statistics) string {
	if s.Comparable == 0 {
		return "insufficient shadow data for a recommendation"
	}
	best, rate := topProvider(s.ProviderWinRate)
	switch {
	case s.AvgSimilarity >= 0.8 && s.AvgCostDifference < 0:
		return fmt.Sprintf("shadow responses match closely (similarity %.2f) at lower cost (avg %.4f/req); consider promoting %q (win rate %.0f%%)",
			s.AvgSimilarity, s.AvgCostDifference, best, rate*100)
	case s.AvgSimilarity < 0.5:
		return fmt.Sprintf("shadow responses diverge from primary (similarity %.2f); keep the primary provider", s.AvgSimilarity)
	default:
		return fmt.Sprintf("no decisive advantage (similarity %.2f, cost diff %.4f/req); keep the primary provider", s.AvgSimilarity, s.AvgCostDifference)
	}
}

// topProvider returns the provider with the highest win rate (ties broken by name).
func topProvider(rates map[string]float64) (string, float64) {
	names := make([]string, 0, len(rates))
	for n := range rates {
		names = append(names, n)
	}
	sort.Strings(names)
	best, bestRate := "", -1.0
	for _, n := range names {
		if rates[n] > bestRate {
			best, bestRate = n, rates[n]
		}
	}
	if bestRate < 0 {
		bestRate = 0
	}
	return best, bestRate
}

func copyIntMap(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyFloatMap(m map[string]float64) map[string]float64 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
