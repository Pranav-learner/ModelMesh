package routing_test

import (
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/routing"
)

func TestMetricsCollector(t *testing.T) {
	c := routing.NewMetricsCollector()

	c.RecordDecision(routing.DecisionRecord{Provider: "openai", Score: 0.8, Duration: 10 * time.Millisecond})
	c.RecordDecision(routing.DecisionRecord{Provider: "openai", Score: 0.6, Duration: 30 * time.Millisecond})
	c.RecordDecision(routing.DecisionRecord{Provider: "anthropic", Score: 0.7, Duration: 20 * time.Millisecond, Fallback: true})
	c.RecordDecision(routing.DecisionRecord{Failed: true, Duration: 20 * time.Millisecond})

	s := c.Snapshot()
	if s.TotalDecisions != 4 {
		t.Errorf("TotalDecisions = %d, want 4", s.TotalDecisions)
	}
	if s.FailedAttempts != 1 {
		t.Errorf("FailedAttempts = %d, want 1", s.FailedAttempts)
	}
	if s.FallbackCount != 1 {
		t.Errorf("FallbackCount = %d, want 1", s.FallbackCount)
	}
	if s.SelectionsPerProvider["openai"] != 2 || s.SelectionsPerProvider["anthropic"] != 1 {
		t.Errorf("SelectionsPerProvider = %v", s.SelectionsPerProvider)
	}
	// Average decision time over all 4 attempts = (10+30+20+20)/4 = 20ms.
	if s.AverageDecisionTime != 20*time.Millisecond {
		t.Errorf("AverageDecisionTime = %s, want 20ms", s.AverageDecisionTime)
	}
	// Average score over the 3 successful selections = (0.8+0.6+0.7)/3 = 0.7.
	if s.AverageScore != 0.7 {
		t.Errorf("AverageScore = %v, want 0.7", s.AverageScore)
	}
}

func TestMetricsCollector_Empty(t *testing.T) {
	s := routing.NewMetricsCollector().Snapshot()
	if s.TotalDecisions != 0 || s.AverageDecisionTime != 0 || s.AverageScore != 0 {
		t.Errorf("empty snapshot not zero: %+v", s)
	}
}

func TestMetricsSnapshot_IsACopy(t *testing.T) {
	c := routing.NewMetricsCollector()
	c.RecordDecision(routing.DecisionRecord{Provider: "openai"})
	s := c.Snapshot()
	s.SelectionsPerProvider["openai"] = 999 // mutate the returned map
	if c.Snapshot().SelectionsPerProvider["openai"] != 1 {
		t.Errorf("Snapshot map is not a copy; mutation leaked into collector")
	}
}

func TestNopMetrics(t *testing.T) {
	// Must be safe to call and not panic.
	routing.NopMetrics{}.RecordDecision(routing.DecisionRecord{Provider: "x"})
}
