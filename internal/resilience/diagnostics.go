package resilience

import (
	"fmt"
	"strings"
)

// This file provides human-readable diagnostics for the resilience subsystem:
// explaining provider rejections and failover decisions. Breaker states come
// from Manager.States() and provider health from Registry.Records(); the
// renderers below combine them into an explanation.

// ExplainFailover renders a failover attempt: which candidates were skipped or
// failed and why, and which one served the request.
func ExplainFailover(o FailoverOutcome) string {
	var b strings.Builder
	if o.Succeeded {
		fmt.Fprintf(&b, "served by %s/%s (failover=%t) after %d attempt(s):\n",
			o.Served.Provider, o.Served.Model, o.FailoverUsed, len(o.Attempts))
	} else {
		fmt.Fprintf(&b, "all %d candidate(s) failed or unavailable:\n", len(o.Attempts))
	}
	for i, a := range o.Attempts {
		b.WriteString("  " + ExplainAttempt(a, i+1) + "\n")
	}
	return b.String()
}

// ExplainAttempt renders a single failover attempt.
func ExplainAttempt(a AttemptResult, rank int) string {
	switch {
	case a.Skipped:
		return fmt.Sprintf("#%d %s/%s SKIPPED (%s)", rank, a.Target.Provider, a.Target.Model, a.Reason)
	case a.Err != nil:
		return fmt.Sprintf("#%d %s/%s FAILED (%v)", rank, a.Target.Provider, a.Target.Model, a.Err)
	default:
		return fmt.Sprintf("#%d %s/%s OK", rank, a.Target.Provider, a.Target.Model)
	}
}

// ExplainStates renders the breaker state of every known provider.
func (m *Manager) ExplainStates() string {
	states := m.States()
	names := make([]string, 0, len(states))
	for name := range states {
		names = append(names, name)
	}
	// stable order
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s=%s ", name, states[name])
	}
	return strings.TrimSpace(b.String())
}
