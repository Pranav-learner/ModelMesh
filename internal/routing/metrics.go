package routing

import (
	"sync"
	"time"
)

// This file is the routing metrics foundation. It defines a small internal
// metrics interface and an in-memory collector. It deliberately does NOT depend
// on Prometheus or any exporter — the Observability phase will export these
// counters through the Metrics interface without changing the router.

// DecisionRecord is a single routing outcome reported to a Metrics sink.
type DecisionRecord struct {
	Provider   string        // selected provider ("" if the attempt failed)
	Model      string        // selected model
	Score      float64       // final score of the selected candidate
	Duration   time.Duration // wall-clock time to reach the decision
	Fallback   bool          // whether a non-top candidate was selected
	Failed     bool          // whether routing failed to select any provider
	Candidates int           // number of candidates considered
}

// Metrics receives routing decision records. Implementations must be safe for
// concurrent use.
type Metrics interface {
	RecordDecision(DecisionRecord)
}

// Compile-time assertions.
var (
	_ Metrics = NopMetrics{}
	_ Metrics = (*MetricsCollector)(nil)
)

// NopMetrics is a Metrics sink that discards everything. It is the safe default
// so the router never depends on a metrics backend being configured.
type NopMetrics struct{}

// RecordDecision discards the record.
func (NopMetrics) RecordDecision(DecisionRecord) {}

// MetricsCollector is an in-memory, concurrency-safe Metrics implementation that
// aggregates routing counters for inspection via Snapshot. Future phases read
// these through the Metrics interface and export them.
type MetricsCollector struct {
	mu            sync.Mutex
	total         int
	failed        int
	fallbacks     int
	perProvider   map[string]int
	totalDuration time.Duration
	totalScore    float64
	scored        int
}

// NewMetricsCollector returns an empty collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{perProvider: make(map[string]int)}
}

// RecordDecision aggregates a routing outcome.
func (c *MetricsCollector) RecordDecision(r DecisionRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.total++
	c.totalDuration += r.Duration
	if r.Failed {
		c.failed++
		return
	}
	if r.Provider != "" {
		c.perProvider[r.Provider]++
	}
	c.totalScore += r.Score
	c.scored++
	if r.Fallback {
		c.fallbacks++
	}
}

// MetricsSnapshot is an immutable view of the collected routing counters.
type MetricsSnapshot struct {
	TotalDecisions        int            `json:"total_decisions"`
	FailedAttempts        int            `json:"failed_attempts"`
	FallbackCount         int            `json:"fallback_count"`
	SelectionsPerProvider map[string]int `json:"selections_per_provider"`
	AverageDecisionTime   time.Duration  `json:"average_decision_time"`
	AverageScore          float64        `json:"average_score"`
}

// Snapshot returns the current aggregated metrics. Averages are computed over the
// relevant denominators (decision time over all attempts; score over successful
// selections).
func (c *MetricsCollector) Snapshot() MetricsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	perProvider := make(map[string]int, len(c.perProvider))
	for k, v := range c.perProvider {
		perProvider[k] = v
	}

	var avgDuration time.Duration
	if c.total > 0 {
		avgDuration = c.totalDuration / time.Duration(c.total)
	}
	var avgScore float64
	if c.scored > 0 {
		avgScore = c.totalScore / float64(c.scored)
	}

	return MetricsSnapshot{
		TotalDecisions:        c.total,
		FailedAttempts:        c.failed,
		FallbackCount:         c.fallbacks,
		SelectionsPerProvider: perProvider,
		AverageDecisionTime:   avgDuration,
		AverageScore:          round4(avgScore),
	}
}
