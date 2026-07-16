package analysis

import "context"

// Attribute keys the analysis framework projects onto RoutingContext.Attributes.
// The token keys intentionally match the routing engine's recognized keys
// (routing.AttrEstimatedInputTokens / AttrEstimatedOutputTokens) so the existing
// cost scorer consumes the analyzed estimates with no routing change. The
// remaining keys are forward-compatible signals for later phases (e.g. complexity
// classification and adaptive routing).
const (
	AttrEstimatedInputTokens  = "estimated_input_tokens"
	AttrEstimatedOutputTokens = "estimated_output_tokens"
	AttrHasCode               = "has_code"
	AttrHasMath               = "has_math"
	AttrHasStructuredData     = "has_structured_data"
	AttrConversationTurns     = "conversation_turns"
	AttrLongContext           = "long_context"
	AttrMultiTurn             = "multi_turn"

	// Part 2: complexity + classification-driven routing hints.
	AttrComplexity         = "complexity"
	AttrPreferredModelTier = "preferred_model_tier"
	AttrPreferredProvider  = "preferred_provider"
	AttrLatencySensitive   = "latency_sensitive"
	AttrCostSensitive      = "cost_sensitive"
	AttrHighContext        = "high_context"
	AttrReasoningIntensive = "reasoning_intensive"
)

// Attributes projects the routing hints onto a flat attribute bag ready to merge
// into RoutingContext.Attributes. The composition layer does the merge, so the
// analysis package stays independent of the routing package.
func (r AnalysisResult) Attributes() map[string]any {
	attrs := map[string]any{
		AttrEstimatedInputTokens:  r.Hints.EstimatedInputTokens,
		AttrEstimatedOutputTokens: r.Hints.EstimatedOutputTokens,
		AttrHasCode:               r.Hints.HasCode,
		AttrHasMath:               r.Hints.HasMath,
		AttrHasStructuredData:     r.Hints.HasStructuredData,
		AttrConversationTurns:     r.Hints.ConversationTurns,
		AttrLongContext:           r.Hints.LongContext,
		AttrMultiTurn:             r.Hints.MultiTurn,
		AttrComplexity:            string(r.Hints.Complexity),
		AttrPreferredModelTier:    string(r.Hints.PreferredModelTier),
		AttrLatencySensitive:      r.Hints.LatencySensitive,
		AttrCostSensitive:         r.Hints.CostSensitive,
		AttrHighContext:           r.Hints.HighContext,
		AttrReasoningIntensive:    r.Hints.ReasoningIntensive,
	}
	if r.Hints.PreferredProvider != "" {
		attrs[AttrPreferredProvider] = r.Hints.PreferredProvider
	}
	return attrs
}

// contextKey is the private type for storing an AnalysisResult in a context.
type contextKey struct{}

// NewContext returns a copy of ctx carrying the analysis result, so downstream
// stages (routing-context construction) can enrich themselves without threading
// the result through every signature.
func NewContext(ctx context.Context, result AnalysisResult) context.Context {
	return context.WithValue(ctx, contextKey{}, result)
}

// FromContext returns the analysis result stored in ctx, if any.
func FromContext(ctx context.Context) (AnalysisResult, bool) {
	r, ok := ctx.Value(contextKey{}).(AnalysisResult)
	return r, ok
}
