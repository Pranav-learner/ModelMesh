package adaptive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// ExplainClassification renders the complexity classification and its rationale
// (re-exporting the analysis explanation for a unified diagnostics surface).
func ExplainClassification(res analysis.AnalysisResult) string {
	return res.Explain()
}

// ExplainRoutingHints renders the generated routing hints.
func ExplainRoutingHints(h analysis.RoutingHints) string {
	parts := []string{
		fmt.Sprintf("complexity=%s", h.Complexity),
		fmt.Sprintf("tier=%s", h.PreferredModelTier),
	}
	if h.PreferredProvider != "" {
		parts = append(parts, "provider="+h.PreferredProvider)
	}
	for _, f := range []struct {
		on   bool
		name string
	}{
		{h.LatencySensitive, "latency-sensitive"},
		{h.CostSensitive, "cost-sensitive"},
		{h.HighContext, "high-context"},
		{h.ReasoningIntensive, "reasoning-intensive"},
	} {
		if f.on {
			parts = append(parts, f.name)
		}
	}
	return "Routing hints: " + strings.Join(parts, ", ")
}

// ExplainAdaptiveWeighting renders the per-request weight adaptation: the base and
// adjusted weights side by side, and the reason for each change.
func ExplainAdaptiveWeighting(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Adaptive weighting (%s):\n", r.Complexity)
	fmt.Fprintf(&b, "  base     cost=%.2f latency=%.2f availability=%.2f quality=%.2f\n",
		r.Base.Cost, r.Base.Latency, r.Base.Availability, r.Base.Quality)
	fmt.Fprintf(&b, "  adjusted cost=%.2f latency=%.2f availability=%.2f quality=%.2f\n",
		r.Adjusted.Cost, r.Adjusted.Latency, r.Adjusted.Availability, r.Adjusted.Quality)
	if !r.Changed() {
		b.WriteString("  (no adjustments — using base weights)\n")
		return b.String()
	}
	for _, a := range r.Adjustments {
		fmt.Fprintf(&b, "  - %s: %.2f → %.2f (%s)\n", a.Factor, a.From, a.To, a.Reason)
	}
	return b.String()
}

// ShowRoutingDecision renders the final routing decision: the selected candidate,
// its score and reason, the effective factor weights, and the ranked field.
func ShowRoutingDecision(d routing.RoutingDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Routing decision [%s]: %s/%s (score %.3f)\n",
		d.Strategy, d.Selected.Provider, d.Selected.Model, d.Selected.Score)
	fmt.Fprintf(&b, "  reason: %s\n", d.Selected.Reason)
	if len(d.Explanation.Weights) > 0 {
		fmt.Fprintf(&b, "  base weights: %s\n", formatWeights(d.Explanation.Weights))
	}
	if len(d.Candidates) > 1 {
		b.WriteString("  candidates:\n")
		for i, c := range d.Candidates {
			marker := " "
			if i == 0 {
				marker = "*"
			}
			fmt.Fprintf(&b, "   %s %s/%s score=%.3f\n", marker, c.Provider, c.Model, c.Score)
		}
	}
	return b.String()
}

func formatWeights(w map[string]float64) string {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%.2f", k, w[k])
	}
	return strings.Join(parts, " ")
}
