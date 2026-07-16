package analysis

import (
	"strings"
	"unicode/utf8"
)

// Extractor is a modular feature detector. Each extractor reads the preprocessed
// prompt and populates part of PromptFeatures. New signals are added by
// implementing a new Extractor and registering it — the engine, the models, and
// existing extractors are untouched.
//
// An extractor must be pure: given the same Preprocessed input it writes the same
// fields, and it only writes the fields it owns.
type Extractor interface {
	// Name returns the stable extractor identifier (e.g. "length").
	Name() string
	// Extract populates the fields it owns on f from p.
	Extract(p Preprocessed, f *PromptFeatures)
}

// DefaultExtractors returns the built-in extractor set in a stable order.
func DefaultExtractors() []Extractor {
	return []Extractor{
		LengthExtractor{},
		CodeExtractor{},
		MathExtractor{},
		StructuredDataExtractor{},
		InstructionExtractor{},
		ReasoningExtractor{},
	}
}

// LengthExtractor computes the size and shape signals: prompt/char/word counts,
// message and system-prompt counts, and conversation history length.
type LengthExtractor struct{}

// Name returns the extractor identifier.
func (LengthExtractor) Name() string { return "length" }

// Extract fills the length/shape features.
func (LengthExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	f.PromptLength = utf8.RuneCountInString(p.Prompt)
	f.CharCount = utf8.RuneCountInString(p.Text)
	f.WordCount = len(strings.Fields(p.Text))
	f.MessageCount = p.MessageCount
	f.SystemPromptCount = p.SystemTurns
	if p.MessageCount > 1 {
		f.ConversationHistoryLength = p.MessageCount - 1
	}
}
