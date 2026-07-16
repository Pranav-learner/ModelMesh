package adaptive

import "sync"

// Metrics is the resource-metrics seam for request-aware routing. It mirrors the
// rest of ModelMesh: the weigher depends on this small interface, not on
// Prometheus, so it is observable without any backend and the composition layer
// can bridge it into the catalog.
type Metrics interface {
	// Classification records one classified request by complexity label.
	Classification(complexity string)
	// HintUsed records that a routing hint influenced the weights.
	HintUsed(hint string)
	// WeightChange records one adaptive factor-weight change.
	WeightChange(factor string, from, to float64)
	// RoutingOutcome records whether the chosen model's tier matched the
	// recommended tier (routing accuracy).
	RoutingOutcome(preferredTier, chosenTier string, matched bool)
}

// NopMetrics is the no-op Metrics used by default.
type NopMetrics struct{}

func (NopMetrics) Classification(string)                 {}
func (NopMetrics) HintUsed(string)                       {}
func (NopMetrics) WeightChange(string, float64, float64) {}
func (NopMetrics) RoutingOutcome(string, string, bool)   {}

// complexityScore maps a complexity label to a numeric value for averaging.
func complexityScore(label string) float64 {
	switch label {
	case "simple":
		return 1
	case "medium":
		return 2
	case "complex":
		return 3
	default:
		return 0
	}
}

// Collector is an in-memory Metrics implementation that aggregates the
// request-aware routing metrics into a serializable Snapshot. It is safe for
// concurrent use.
type Collector struct {
	mu            sync.Mutex
	total         int
	distribution  map[string]int
	complexitySum float64
	hintUsage     map[string]int
	weightChanges int
	accuracyHits  int
	accuracyTotal int
}

// NewCollector returns an empty metrics collector.
func NewCollector() *Collector {
	return &Collector{distribution: map[string]int{}, hintUsage: map[string]int{}}
}

// Classification records a classified request.
func (c *Collector) Classification(complexity string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total++
	c.distribution[complexity]++
	c.complexitySum += complexityScore(complexity)
}

// HintUsed records a hint influence.
func (c *Collector) HintUsed(hint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hintUsage[hint]++
}

// WeightChange records an adaptive weight change.
func (c *Collector) WeightChange(string, float64, float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.weightChanges++
}

// RoutingOutcome records a routing-accuracy sample.
func (c *Collector) RoutingOutcome(_, _ string, matched bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accuracyTotal++
	if matched {
		c.accuracyHits++
	}
}

// Snapshot is a point-in-time view of the aggregated metrics.
type Snapshot struct {
	Total             int            `json:"total"`
	Distribution      map[string]int `json:"distribution"`
	AverageComplexity float64        `json:"average_complexity"`
	HintUsage         map[string]int `json:"hint_usage"`
	WeightChanges     int            `json:"weight_changes"`
	Accuracy          float64        `json:"routing_accuracy"`
	AccuracySamples   int            `json:"routing_accuracy_samples"`
}

// Snapshot returns the current aggregated metrics.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	dist := make(map[string]int, len(c.distribution))
	for k, v := range c.distribution {
		dist[k] = v
	}
	hints := make(map[string]int, len(c.hintUsage))
	for k, v := range c.hintUsage {
		hints[k] = v
	}
	var avg, acc float64
	if c.total > 0 {
		avg = c.complexitySum / float64(c.total)
	}
	if c.accuracyTotal > 0 {
		acc = float64(c.accuracyHits) / float64(c.accuracyTotal)
	}
	return Snapshot{
		Total:             c.total,
		Distribution:      dist,
		AverageComplexity: avg,
		HintUsage:         hints,
		WeightChanges:     c.weightChanges,
		Accuracy:          acc,
		AccuracySamples:   c.accuracyTotal,
	}
}
