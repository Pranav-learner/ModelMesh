package analysis

// Complexity is the classified difficulty of a prompt. It is a deliberately small,
// ordered vocabulary the routing engine (Part 3) will map to model tiers.
type Complexity string

const (
	ComplexitySimple  Complexity = "simple"
	ComplexityMedium  Complexity = "medium"
	ComplexityComplex Complexity = "complex"
)

// String returns the complexity label.
func (c Complexity) String() string { return string(c) }

// ModelTier is a coarse, provider-agnostic model capability tier. The classifier
// recommends a tier; the routing engine maps tiers to concrete models.
type ModelTier string

const (
	TierSmall    ModelTier = "small"    // fast, cheap models for simple prompts
	TierStandard ModelTier = "standard" // balanced models for medium prompts
	TierLarge    ModelTier = "large"    // frontier models for complex prompts
)

// TriggeredRule records a single rule that fired during classification, for
// explainability.
type TriggeredRule struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Weight      float64 `json:"weight"`
}

// Classification is the explainable result of complexity classification: the
// verdict plus everything needed to justify it — the features that informed it,
// the rules that fired, the confidence, and the reasons for the generated hints.
type Classification struct {
	// Complexity is the classified label.
	Complexity Complexity `json:"complexity"`
	// Score is the total weight of the triggered rules.
	Score float64 `json:"score"`
	// Confidence is how decisively the score falls within its band, in [0,1].
	Confidence float64 `json:"confidence"`
	// FeaturesUsed lists the distinct feature signals the triggered rules read.
	FeaturesUsed []string `json:"features_used"`
	// TriggeredRules lists the rules that fired, in rule-set order.
	TriggeredRules []TriggeredRule `json:"triggered_rules"`
	// HintReasons explains, in order, why each routing hint was generated.
	HintReasons []string `json:"hint_reasons"`
}
