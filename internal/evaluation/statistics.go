package evaluation

import (
	"math"
	"time"
)

// Statistics aggregates the stored evaluation records into averages and per-
// provider efficiency win rates. Only comparable records (successful shadows)
// contribute to the averages.
func (e *Engine) Statistics() Statistics {
	records := e.store.all()
	stats := Statistics{Records: len(records), ProviderWinRate: map[string]float64{}}

	var sumLatency time.Duration
	var sumCost, sumTokens, sumSim float64
	var exactMatches int
	appearances := map[string]int{}
	wins := map[string]int{}

	for _, r := range records {
		if !r.Comparable {
			continue
		}
		stats.Comparable++
		c := r.Comparison

		sumLatency += c.Latency.Difference
		sumCost += c.Cost.Difference
		sumTokens += float64(c.Cost.TokenDifference)
		sumSim += c.Quality.TextSimilarity
		if c.Quality.ExactMatch {
			exactMatches++
		}

		appearances[c.PrimaryProvider]++
		appearances[c.ShadowProvider]++
		switch c.Winner {
		case WinnerPrimary:
			wins[c.PrimaryProvider]++
		case WinnerShadow:
			wins[c.ShadowProvider]++
		}
	}

	if n := stats.Comparable; n > 0 {
		stats.AvgLatencyDifference = sumLatency / time.Duration(n)
		stats.AvgCostDifference = round4(sumCost / float64(n))
		stats.AvgTokenDifference = round4(sumTokens / float64(n))
		stats.AvgSimilarity = round4(sumSim / float64(n))
		stats.ExactMatchRate = round4(float64(exactMatches) / float64(n))
	}
	for prov, appears := range appearances {
		if appears > 0 {
			stats.ProviderWinRate[prov] = round4(float64(wins[prov]) / float64(appears))
		}
	}
	return stats
}

func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
