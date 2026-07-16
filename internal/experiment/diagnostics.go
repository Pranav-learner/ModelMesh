package experiment

import (
	"fmt"
	"sort"
	"strings"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// ExplainExperiment renders an experiment's headline report as operator-readable
// text: provider comparison, savings, classification, and the recommendation.
func ExplainExperiment(e *Experiment) string {
	r := e.Report()
	var b strings.Builder
	fmt.Fprintf(&b, "Experiment %q", r.Experiment)
	if r.Description != "" {
		fmt.Fprintf(&b, " — %s", r.Description)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "  evaluations: %d (comparable %d, shadow sampled %d)\n", r.Evaluations, r.Comparable, r.ShadowSampled)
	fmt.Fprintf(&b, "  avg similarity: %.3f   exact-match rate: %.0f%%\n", r.AvgSimilarity, r.ExactMatchRate*100)
	fmt.Fprintf(&b, "  avg cost diff: %.4f/req   avg latency diff: %s\n", r.AvgCostDifference, r.AvgLatencyDifference)
	if len(r.ProviderWinRate) > 0 {
		fmt.Fprintf(&b, "  provider win rate: %s\n", formatFloatMap(r.ProviderWinRate))
	}
	fmt.Fprintf(&b, "  savings: cache $%.4f + budget $%.4f  → est. monthly $%.2f\n",
		r.CacheSavingsUSD, r.BudgetSavingsUSD, r.EstimatedMonthlySavingsUSD)
	fmt.Fprintf(&b, "  recommendation: %s\n", r.Recommendation)
	return b.String()
}

// InspectComparison renders one evaluation record in detail.
func InspectComparison(rec evaluation.EvaluationRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", rec.ID, rec.CorrelationID)
	if !rec.Comparable {
		fmt.Fprintf(&b, " — shadow failed: %s\n", rec.ShadowError)
		return b.String()
	}
	c := rec.Comparison
	fmt.Fprintf(&b, "\n  primary: %s/%s   shadow: %s/%s   winner: %s\n",
		c.PrimaryProvider, c.PrimaryModel, c.ShadowProvider, c.ShadowModel, c.Winner)
	fmt.Fprintf(&b, "  quality: exact=%t similarity=%.3f finish=%t (len %d vs %d)\n",
		c.Quality.ExactMatch, c.Quality.TextSimilarity, c.Quality.FinishReasonMatch, c.Quality.PrimaryLength, c.Quality.ShadowLength)
	fmt.Fprintf(&b, "  latency: %s vs %s (Δ %s)\n", c.Latency.PrimaryLatency, c.Latency.ShadowLatency, c.Latency.Difference)
	fmt.Fprintf(&b, "  cost: $%.4f vs $%.4f (Δ $%.4f, %d vs %d tokens)\n",
		c.Cost.PrimaryCost, c.Cost.ShadowCost, c.Cost.Difference, c.Cost.PrimaryTokens, c.Cost.ShadowTokens)
	return b.String()
}

// EvaluationHistory renders the most recent evaluation records as a compact table.
// A non-positive limit shows all records.
func EvaluationHistory(records []evaluation.EvaluationRecord, limit int) string {
	if limit > 0 && len(records) > limit {
		records = records[len(records)-limit:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-14s %-22s %-22s %-8s %-10s\n", "CORRELATION", "PRIMARY", "SHADOW", "WINNER", "SIMILARITY")
	b.WriteString(strings.Repeat("-", 80) + "\n")
	for _, r := range records {
		if !r.Comparable {
			fmt.Fprintf(&b, "%-14s %-22s %-22s %-8s %-10s\n", trunc(r.CorrelationID, 14), "(shadow failed)", "", "-", "-")
			continue
		}
		c := r.Comparison
		fmt.Fprintf(&b, "%-14s %-22s %-22s %-8s %.3f\n",
			trunc(r.CorrelationID, 14),
			trunc(c.PrimaryProvider+"/"+c.PrimaryModel, 22),
			trunc(c.ShadowProvider+"/"+c.ShadowModel, 22),
			c.Winner, c.Quality.TextSimilarity)
	}
	return b.String()
}

// ShowRoutingDecision renders a routing decision (re-exporting the adaptive-layer
// diagnostic for a unified experimentation surface).
func ShowRoutingDecision(d routing.RoutingDecision) string {
	return adaptive.ShowRoutingDecision(d)
}

func formatFloatMap(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%.0f%%", k, m[k]*100)
	}
	return strings.Join(parts, " ")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
