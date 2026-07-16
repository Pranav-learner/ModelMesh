package analysis

import "github.com/symbiotes/modelmesh/internal/provider"

// Preprocessed is the cleaned, structured view of a request's prompt produced by
// the Preprocessor and consumed by the feature extractors and token estimator.
type Preprocessed struct {
	// Messages is the whitespace-normalized copy of the request messages.
	Messages []provider.ChatMessage `json:"messages"`
	// Text is the normalized concatenation of all message contents, used for
	// feature extraction and token estimation.
	Text string `json:"-"`
	// Prompt is the normalized content of the latest user message (the actual
	// request being made), separate from the surrounding conversation.
	Prompt string `json:"-"`
	// SystemPrompts holds the contents of every system message, in order.
	SystemPrompts []string `json:"system_prompts,omitempty"`
	// Message counts by role plus the total.
	MessageCount   int `json:"message_count"`
	UserTurns      int `json:"user_turns"`
	AssistantTurns int `json:"assistant_turns"`
	SystemTurns    int `json:"system_turns"`
}

// PromptFeatures is the structured set of signals extracted from a request. It is
// deliberately flat and provider-agnostic; new signals are added as fields and
// populated by a new Extractor.
type PromptFeatures struct {
	// PromptLength is the character length of the latest user prompt.
	PromptLength int `json:"prompt_length"`
	// CharCount is the total character count across all normalized messages.
	CharCount int `json:"char_count"`
	// WordCount is the total word count across all normalized messages.
	WordCount int `json:"word_count"`
	// MessageCount is the number of messages in the request.
	MessageCount int `json:"message_count"`
	// EstimatedContextSize is the estimated token size of the full input context.
	EstimatedContextSize int `json:"estimated_context_size"`
	// HasCode reports whether the prompt appears to contain source code.
	HasCode bool `json:"has_code"`
	// HasMath reports whether the prompt appears to contain mathematical content.
	HasMath bool `json:"has_math"`
	// HasStructuredData reports whether the prompt contains structured data
	// (JSON, XML/HTML, YAML, CSV, or a markdown table).
	HasStructuredData bool `json:"has_structured_data"`
	// ConversationHistoryLength is the number of messages preceding the current
	// prompt (the conversation history).
	ConversationHistoryLength int `json:"conversation_history_length"`
	// SystemPromptCount is the number of system messages.
	SystemPromptCount int `json:"system_prompt_count"`
	// InstructionCount is the number of distinct instructions detected (numbered
	// list items, bullets, and imperative sentences).
	InstructionCount int `json:"instruction_count"`
	// ReasoningIndicatorCount is the number of multi-step-reasoning cues detected
	// (e.g. "step by step", "explain why", "compare", "prove").
	ReasoningIndicatorCount int `json:"reasoning_indicator_count"`
}

// TokenEstimate is a lightweight, heuristic token estimate for a request. A future
// phase may replace the estimator that produces it.
type TokenEstimate struct {
	InputTokens          int `json:"input_tokens"`
	ExpectedOutputTokens int `json:"expected_output_tokens"`
	EstimatedTotalTokens int `json:"estimated_total_tokens"`
}

// RoutingHints is the distilled, forward-compatible set of signals the Routing
// Engine (and later phases) consume. It is derived from the features and token
// estimate and is what AnalysisResult.Attributes() projects onto the routing
// context.
type RoutingHints struct {
	EstimatedInputTokens  int  `json:"estimated_input_tokens"`
	EstimatedOutputTokens int  `json:"estimated_output_tokens"`
	HasCode               bool `json:"has_code"`
	HasMath               bool `json:"has_math"`
	HasStructuredData     bool `json:"has_structured_data"`
	// ConversationTurns is the number of messages in the request.
	ConversationTurns int `json:"conversation_turns"`
	// LongContext is true when the input exceeds the configured token threshold.
	LongContext bool `json:"long_context"`
	// MultiTurn is true when the request carries conversation history.
	MultiTurn bool `json:"multi_turn"`

	// --- Part 2: complexity-driven hints ---

	// Complexity is the classified prompt complexity.
	Complexity Complexity `json:"complexity,omitempty"`
	// PreferredModelTier is the model tier the classification recommends.
	PreferredModelTier ModelTier `json:"preferred_model_tier,omitempty"`
	// PreferredProvider is an optional provider recommendation (empty if none).
	PreferredProvider string `json:"preferred_provider,omitempty"`
	// LatencySensitive marks requests that should favor fast, cheap models.
	LatencySensitive bool `json:"latency_sensitive"`
	// CostSensitive marks requests that should favor cost-efficient models.
	CostSensitive bool `json:"cost_sensitive"`
	// HighContext marks requests whose context favors large-context models.
	HighContext bool `json:"high_context"`
	// ReasoningIntensive marks requests that should favor reasoning-capable models.
	ReasoningIntensive bool `json:"reasoning_intensive"`
}

// AnalysisResult is the complete structured analysis of a request, produced once
// per request before routing.
type AnalysisResult struct {
	Preprocessed   Preprocessed   `json:"preprocessed"`
	Features       PromptFeatures `json:"features"`
	Tokens         TokenEstimate  `json:"tokens"`
	Classification Classification `json:"classification"`
	Hints          RoutingHints   `json:"hints"`
}
