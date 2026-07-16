package routing

import "github.com/symbiotes/modelmesh/internal/provider"

// This file defines the routing DTOs. They are provider-agnostic and carry only
// what a routing decision needs — never a provider SDK type.

// Candidate is a routable (provider, model) pair under consideration by the
// router. Weight is configured metadata (used by weighting strategies); Score is
// a computed ranking value that remains zero until scoring is implemented in a
// later phase.
type Candidate struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Weight   float64 `json:"weight"`
	Score    float64 `json:"score"`
	// Factors holds the per-scorer normalized scores that produced Score (keyed by
	// scorer name: "cost", "latency", "availability", "quality"). It is populated
	// by scoring strategies and empty otherwise.
	Factors map[string]float64 `json:"factors,omitempty"`
	// Reason is a short, human-readable note about how this candidate was treated
	// by the active strategy.
	Reason string `json:"reason,omitempty"`
}

// Constraints narrow the candidate set for a single routing decision. Empty
// allow-lists mean "no restriction".
type Constraints struct {
	// Providers restricts routing to these provider names.
	Providers []string `json:"providers,omitempty"`
	// Models restricts routing to these model IDs.
	Models []string `json:"models,omitempty"`
	// AllowFallback signals whether the caller permits using candidates beyond the
	// selected one. The router always returns the full ordered candidate list; this
	// flag records the caller's intent for the dispatch layer and future phases.
	AllowFallback bool `json:"allow_fallback"`
}

func (c Constraints) allowsProvider(name string) bool {
	return allowed(c.Providers, name)
}

func (c Constraints) allowsModel(id string) bool {
	return allowed(c.Models, id)
}

func allowed(list []string, value string) bool {
	if len(list) == 0 {
		return true
	}
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// RoutingContext describes a request to be routed. It is provider-agnostic: it
// captures the capability required, an optional model preference, caller
// constraints, and an open Attributes bag for future signals (e.g. a
// prompt-complexity classification) so new inputs need no contract change.
type RoutingContext struct {
	// Capability is the operation the request needs (chat or embeddings). An empty
	// value is treated as chat.
	Capability provider.Capability `json:"capability"`
	// Model is a requested model/alias, or empty to let the router choose.
	Model string `json:"model,omitempty"`
	// Constraints narrow the candidate set.
	Constraints Constraints `json:"constraints"`
	// Attributes carries optional, forward-compatible routing signals.
	Attributes map[string]any `json:"attributes,omitempty"`
	// RequestID correlates the decision with logs and (later) traces.
	RequestID string `json:"request_id,omitempty"`
}

// ChatContext builds a RoutingContext for a chat request, carrying the requested
// model preference through to the router.
func ChatContext(req provider.ChatRequest) RoutingContext {
	return RoutingContext{Capability: provider.CapabilityChat, Model: req.Model}
}

// EmbeddingContext builds a RoutingContext for an embeddings request.
func EmbeddingContext(req provider.EmbeddingRequest) RoutingContext {
	return RoutingContext{Capability: provider.CapabilityEmbeddings, Model: req.Model}
}

// RoutingDecision is the outcome of routing: the selected candidate, the full
// ordered candidate list (best first, Selected at index 0), the strategy used,
// and a structured explanation.
type RoutingDecision struct {
	Selected    Candidate          `json:"selected"`
	Candidates  []Candidate        `json:"candidates"`
	Strategy    string             `json:"strategy"`
	Explanation RoutingExplanation `json:"explanation"`
}

// RoutingExplanation captures why a decision was made, for debugging and (later)
// the router-explanation API surface. It is both machine-readable (structured
// fields) and human-readable (Reason).
type RoutingExplanation struct {
	Strategy string `json:"strategy"`
	Reason   string `json:"reason"`
	// Weights are the normalized scoring-factor weights used (scorer name -> weight
	// summing to 1). Empty for strategies that do not score.
	Weights    map[string]float64     `json:"weights,omitempty"`
	Considered int                    `json:"considered"`
	Candidates []CandidateExplanation `json:"candidates"`
}

// CandidateExplanation is a per-candidate record within an explanation, carrying
// the full score breakdown so a decision can be audited or rendered.
type CandidateExplanation struct {
	Provider string             `json:"provider"`
	Model    string             `json:"model"`
	Weight   float64            `json:"weight"`
	Factors  map[string]float64 `json:"factors,omitempty"`
	Score    float64            `json:"score"`
	Rank     int                `json:"rank"`
	Selected bool               `json:"selected"`
	Reason   string             `json:"reason,omitempty"`
}
