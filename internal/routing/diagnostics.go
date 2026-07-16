package routing

import (
	"fmt"
	"strings"
)

// This file provides human-readable diagnostics for routing decisions, to make
// debugging routing behavior easy. All functions are pure renderers over the
// routing DTOs — they perform no I/O and hold no state.

// Explain renders a full, human-readable explanation of a routing decision: the
// strategy, factor weights, the ranked candidates with their score breakdowns,
// the winner, and the reason it won.
func Explain(d RoutingDecision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Routing decision [strategy=%s]\n", d.Strategy)
	if len(d.Explanation.Weights) > 0 {
		fmt.Fprintf(&b, "Weights: %s\n", formatFloatMap(d.Explanation.Weights))
	}
	fmt.Fprintf(&b, "Candidates (%d):\n", d.Explanation.Considered)
	b.WriteString(ScoreBreakdown(d))
	fmt.Fprintf(&b, "Winner: %s / %s (final %.3f)\n", d.Selected.Provider, d.Selected.Model, d.Selected.Score)
	fmt.Fprintf(&b, "Reason: %s\n", d.Explanation.Reason)
	return b.String()
}

// Rankings returns the ranked candidate explanations (best first). It is a thin
// accessor so callers need not reach into the explanation structure.
func Rankings(d RoutingDecision) []CandidateExplanation {
	return d.Explanation.Candidates
}

// ScoreBreakdown renders a per-candidate factor table (one line per candidate),
// marking the selected candidate with an asterisk.
func ScoreBreakdown(d RoutingDecision) string {
	var b strings.Builder
	for _, c := range d.Explanation.Candidates {
		marker := " "
		if c.Selected {
			marker = "*"
		}
		fmt.Fprintf(&b, "  %s #%d %-18s %-16s final=%.3f  [%s]\n",
			marker, c.Rank, c.Provider, c.Model, c.Score, formatFloatMap(c.Factors))
	}
	return b.String()
}

// InspectSelection renders the outcome of a Select call: the resolved provider,
// chosen model, score, and whether fallback was used.
func InspectSelection(s *Selection) string {
	if s == nil {
		return "no selection"
	}
	return fmt.Sprintf("selected %s / %s (final %.3f, fallback=%t, attempts=%d, took %s)",
		s.Selected.Provider, s.Selected.Model, s.Selected.Score, s.FallbackUsed, s.Attempts, s.Duration)
}

// formatFloatMap renders a float map as "key=value" pairs in sorted key order,
// so output is deterministic.
func formatFloatMap(m map[string]float64) string {
	keys := sortedKeys(m)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%.2f", k, m[k])
	}
	return strings.Join(parts, " ")
}
