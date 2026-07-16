package evaluation

import (
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Side is one participant in a comparison — the primary or the shadow. It is the
// neutral input to Compare, so the comparison logic is testable without the shadow
// package.
type Side struct {
	Provider string
	Model    string
	Response provider.ChatResponse
	Latency  time.Duration
}

// Winner labels which side a comparison favored on efficiency (cost + latency).
type Winner string

const (
	WinnerPrimary Winner = "primary"
	WinnerShadow  Winner = "shadow"
	WinnerTie     Winner = "tie"
)

// LatencyMetrics compares the two sides' latencies.
type LatencyMetrics struct {
	PrimaryLatency time.Duration `json:"primary_latency"`
	ShadowLatency  time.Duration `json:"shadow_latency"`
	// Difference is shadow − primary (negative means the shadow was faster).
	Difference   time.Duration `json:"difference"`
	ShadowFaster bool          `json:"shadow_faster"`
}

// CostMetrics compares the two sides' cost and token usage.
type CostMetrics struct {
	PrimaryCost float64 `json:"primary_cost"`
	ShadowCost  float64 `json:"shadow_cost"`
	// Difference is shadow − primary (negative means the shadow was cheaper).
	Difference    float64 `json:"difference"`
	ShadowCheaper bool    `json:"shadow_cheaper"`

	PrimaryTokens int `json:"primary_tokens"`
	ShadowTokens  int `json:"shadow_tokens"`
	// TokenDifference is shadow − primary total tokens.
	TokenDifference int `json:"token_difference"`
}

// QualityMetrics captures deterministic, lightweight response-quality signals. It
// measures agreement between responses, not absolute quality (no judge model).
type QualityMetrics struct {
	ExactMatch     bool    `json:"exact_match"`
	TextSimilarity float64 `json:"text_similarity"` // [0,1]
	// EmbeddingSimilarity is populated only when an embedding scorer is configured.
	EmbeddingSimilarity float64 `json:"embedding_similarity,omitempty"`
	HasEmbedding        bool    `json:"has_embedding"`

	PrimaryLength    int `json:"primary_length"` // response character length
	ShadowLength     int `json:"shadow_length"`
	LengthDifference int `json:"length_difference"` // shadow − primary

	PrimaryFinishReason string `json:"primary_finish_reason"`
	ShadowFinishReason  string `json:"shadow_finish_reason"`
	FinishReasonMatch   bool   `json:"finish_reason_match"`
}

// ComparisonResult is the full comparison of a primary/shadow pair.
type ComparisonResult struct {
	PrimaryProvider string `json:"primary_provider"`
	PrimaryModel    string `json:"primary_model"`
	ShadowProvider  string `json:"shadow_provider"`
	ShadowModel     string `json:"shadow_model"`

	Quality QualityMetrics `json:"quality"`
	Latency LatencyMetrics `json:"latency"`
	Cost    CostMetrics    `json:"cost"`

	// Winner is the more efficient side (cheaper + faster); Tie when neither leads.
	Winner Winner `json:"winner"`
}

// EvaluationRecord is a single stored evaluation. When the shadow request failed,
// Comparable is false and only the error is meaningful.
type EvaluationRecord struct {
	ID            string           `json:"id"`
	CorrelationID string           `json:"correlation_id,omitempty"`
	Timestamp     time.Time        `json:"timestamp"`
	Comparable    bool             `json:"comparable"`
	ShadowError   string           `json:"shadow_error,omitempty"`
	Comparison    ComparisonResult `json:"comparison"`
}

// Statistics aggregates the stored evaluation records.
type Statistics struct {
	Records    int `json:"records"`
	Comparable int `json:"comparable"`

	AvgLatencyDifference time.Duration `json:"avg_latency_difference"`
	AvgCostDifference    float64       `json:"avg_cost_difference"`
	AvgTokenDifference   float64       `json:"avg_token_difference"`
	AvgSimilarity        float64       `json:"avg_similarity"`
	ExactMatchRate       float64       `json:"exact_match_rate"`

	// ProviderWinRate maps a provider to its efficiency win rate over the
	// comparisons it appeared in (ties are not wins).
	ProviderWinRate map[string]float64 `json:"provider_win_rate"`
}
