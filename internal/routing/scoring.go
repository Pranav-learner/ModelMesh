package routing

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// Scorer names. Each scorer has exactly one responsibility and one stable name.
const (
	ScorerCost         = "cost"
	ScorerLatency      = "latency"
	ScorerAvailability = "availability"
	ScorerQuality      = "quality"
)

// Scorer computes a normalized score in [0,1] (1 = best) for each candidate. It
// scores the whole candidate set at once so that scorers which normalize relative
// to the set (cost, latency) and scorers which are absolute (availability,
// quality) share one interface. Implementations must return one score per
// candidate, index-aligned with the input.
//
// Adding a new scoring factor is implementing this interface and registering it
// with a weight — the aggregator, ranking, and explanation code never change.
type Scorer interface {
	// Name returns the stable scorer identifier.
	Name() string
	// Scores returns a normalized score per candidate, aligned with candidates.
	Scores(ctx context.Context, rc RoutingContext, candidates []Candidate) ([]float64, error)
}

// Breakdown is the scoring result for a single candidate: its per-factor scores
// and the aggregated final score.
type Breakdown struct {
	Candidate Candidate
	Factors   map[string]float64
	Final     float64
}

// aggregate runs every scorer over the candidate set and combines the per-factor
// scores into a final weighted score per candidate. normWeights must be
// normalized (sum to 1); factors with no weight contribute nothing.
func aggregate(ctx context.Context, rc RoutingContext, candidates []Candidate, scorers []Scorer, normWeights map[string]float64) ([]Breakdown, error) {
	factorScores := make(map[string][]float64, len(scorers))
	for _, s := range scorers {
		scores, err := s.Scores(ctx, rc, candidates)
		if err != nil {
			return nil, fmt.Errorf("routing: scorer %q failed: %w", s.Name(), err)
		}
		if len(scores) != len(candidates) {
			return nil, fmt.Errorf("routing: scorer %q returned %d scores for %d candidates", s.Name(), len(scores), len(candidates))
		}
		factorScores[s.Name()] = scores
	}

	out := make([]Breakdown, len(candidates))
	for i, c := range candidates {
		factors := make(map[string]float64, len(factorScores))
		final := 0.0
		for name, scores := range factorScores {
			factors[name] = scores[i]
			final += normWeights[name] * scores[i]
		}
		out[i] = Breakdown{Candidate: c, Factors: factors, Final: final}
	}
	return out, nil
}

// normalizeWeights scales weights so they sum to 1. If the total is not positive
// it falls back to equal weighting across the provided keys, so the aggregator
// always has a usable weighting (config validation rejects the invalid case
// earlier for user-facing errors).
func normalizeWeights(weights map[string]float64) map[string]float64 {
	total := 0.0
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	out := make(map[string]float64, len(weights))
	if total <= 0 {
		equal := 0.0
		if len(weights) > 0 {
			equal = 1.0 / float64(len(weights))
		}
		for k := range weights {
			out[k] = equal
		}
		return out
	}
	for k, w := range weights {
		if w < 0 {
			w = 0
		}
		out[k] = w / total
	}
	return out
}

// normalizeLowerIsBetter maps raw values to [0,1] where the lowest value scores
// 1.0 and the highest 0.0 (min-max, inverted). This is the shared normalization
// for "cheaper is better" and "faster is better" factors, so that logic is not
// duplicated across scorers.
//
// Degenerate cases — an empty set, a single value, or all-equal values — yield
// 1.0 for every candidate, since there is no basis to prefer one over another.
func normalizeLowerIsBetter(values []float64) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	for i, v := range values {
		if span == 0 {
			out[i] = 1.0
			continue
		}
		out[i] = (max - v) / span
	}
	return out
}

// clamp01 constrains x to [0,1].
func clamp01(x float64) float64 {
	switch {
	case x < 0:
		return 0
	case x > 1:
		return 1
	default:
		return x
	}
}

// round4 rounds to four decimals for stable, readable score output.
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }

// almostEqual reports whether two scores are equal within a small epsilon, used
// to detect ties before deterministic tie-breaking.
func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// estimatedTokens reads a token estimate from the routing context attributes,
// falling back to def. It accepts int, int64, or float64 values.
func estimatedTokens(rc RoutingContext, key string, def int) int {
	if rc.Attributes == nil {
		return def
	}
	switch n := rc.Attributes[key].(type) {
	case int:
		if n > 0 {
			return n
		}
	case int64:
		if n > 0 {
			return int(n)
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	}
	return def
}

// sortedKeys returns the sorted keys of a float map, for deterministic iteration.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
