package analysis

// Signals is the read-only, flattened input to the rule engine and hint
// generator. It is derived from the extracted features and token estimate so
// rules depend on a single stable shape rather than on the feature/token structs.
type Signals struct {
	PromptLength         int
	CharCount            int
	WordCount            int
	MessageCount         int
	InputTokens          int
	ExpectedOutputTokens int
	TotalTokens          int
	HasCode              bool
	HasMath              bool
	HasStructuredData    bool
	InstructionCount     int
	ReasoningIndicators  int
	ConversationHistory  int
	LongContext          bool
	MultiTurn            bool
}

// signalsFrom builds the Signals for classification from the features, token
// estimate, and the long-context threshold.
func signalsFrom(f PromptFeatures, t TokenEstimate, longContextTokens int) Signals {
	return Signals{
		PromptLength:         f.PromptLength,
		CharCount:            f.CharCount,
		WordCount:            f.WordCount,
		MessageCount:         f.MessageCount,
		InputTokens:          t.InputTokens,
		ExpectedOutputTokens: t.ExpectedOutputTokens,
		TotalTokens:          t.EstimatedTotalTokens,
		HasCode:              f.HasCode,
		HasMath:              f.HasMath,
		HasStructuredData:    f.HasStructuredData,
		InstructionCount:     f.InstructionCount,
		ReasoningIndicators:  f.ReasoningIndicatorCount,
		ConversationHistory:  f.ConversationHistoryLength,
		LongContext:          t.InputTokens >= longContextTokens,
		MultiTurn:            f.ConversationHistoryLength > 0,
	}
}
