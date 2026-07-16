package analysis

import (
	"fmt"
	"strings"
)

// Explain renders a human-readable explanation of the classification: the
// complexity verdict and confidence, the features used, the rules that triggered
// (with weights), and the generated routing hints with their reasons. It is the
// operator-facing counterpart of the structured Classification.
func (r AnalysisResult) Explain() string {
	c := r.Classification
	var b strings.Builder

	fmt.Fprintf(&b, "Complexity: %s (score %.1f, confidence %.0f%%)\n",
		c.Complexity, c.Score, c.Confidence*100)

	fmt.Fprintf(&b, "Features used: %s\n", joinOrNone(c.FeaturesUsed))

	b.WriteString("Rules triggered:\n")
	if len(c.TriggeredRules) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, tr := range c.TriggeredRules {
		fmt.Fprintf(&b, "  - %s (+%.1f): %s\n", tr.Name, tr.Weight, tr.Description)
	}

	fmt.Fprintf(&b, "Generated hints: %s\n", r.hintSummary())
	if len(c.HintReasons) > 0 {
		for _, reason := range c.HintReasons {
			fmt.Fprintf(&b, "  - %s\n", reason)
		}
	}
	return b.String()
}

// hintSummary renders the active routing hints as a compact list.
func (r AnalysisResult) hintSummary() string {
	h := r.Hints
	parts := []string{fmt.Sprintf("tier=%s", h.PreferredModelTier)}
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
	return strings.Join(parts, ", ")
}

func joinOrNone(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}
