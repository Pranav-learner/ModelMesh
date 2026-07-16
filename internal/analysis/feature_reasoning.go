package analysis

import "regexp"

// reasoningRe matches multi-step / deep-reasoning cues. Each distinct match is a
// signal that the request wants analysis rather than a lookup answer.
var reasoningRe = regexp.MustCompile(`(?i)\b(step[ -]by[ -]step|think through|chain[ -]of[ -]thought|reason(ing)?|explain why|justify|derive|prove|analyze|analyse|evaluate|critique|compare|contrast|trade[ -]?offs?|pros and cons|implications|step \d|first.*then|why does|how does|what if|walk me through)\b`)

// ReasoningExtractor counts multi-step-reasoning indicators in a prompt. It is a
// deterministic keyword heuristic, not an inference of actual difficulty.
type ReasoningExtractor struct{}

// Name returns the extractor identifier.
func (ReasoningExtractor) Name() string { return "reasoning" }

// Extract sets ReasoningIndicatorCount.
func (ReasoningExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	f.ReasoningIndicatorCount = len(reasoningRe.FindAllString(p.Text, -1))
}
