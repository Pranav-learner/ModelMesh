package analysis

import (
	"regexp"
	"strings"
)

var (
	// numberedItemRe matches a numbered list item ("1. ", "2) ").
	numberedItemRe = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+\S`)
	// bulletItemRe matches a bullet list item ("- ", "* ", "• ").
	bulletItemRe = regexp.MustCompile(`(?m)^\s*[-*•]\s+\S`)
	// imperativeVerbRe matches an imperative verb that opens a clause: at the start
	// of the prompt, after sentence punctuation / a comma / newline, or after a
	// sequencing connector ("then", "also", "next", "finally", "and"). This counts
	// conjunctive multi-step requests ("write X, then translate Y, and summarize Z")
	// as separate instructions.
	imperativeVerbRe = regexp.MustCompile(`(?i)(?:^|[.!?,\n]\s*|\b(?:then|also|next|finally|and)\s+)(write|create|explain|list|summarize|summarise|compare|implement|generate|describe|calculate|analyze|analyse|translate|fix|refactor|design|build|define|outline|derive|prove|evaluate|convert|optimize|optimise|debug)\b`)
)

// InstructionExtractor counts the distinct instructions in a prompt: numbered list
// items, bullet items, and imperative-verb sentence openers. It is a deterministic
// heuristic — a proxy for "how many things is the user asking for".
type InstructionExtractor struct{}

// Name returns the extractor identifier.
func (InstructionExtractor) Name() string { return "instructions" }

// Extract sets InstructionCount.
func (InstructionExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	text := p.Text
	count := len(numberedItemRe.FindAllString(text, -1))
	count += len(bulletItemRe.FindAllString(text, -1))
	count += len(imperativeVerbRe.FindAllString(text, -1))
	// A single imperative sentence is one instruction; ensure a non-empty prompt
	// with no list/verb still counts as at least one instruction.
	if count == 0 && strings.TrimSpace(text) != "" {
		count = 1
	}
	f.InstructionCount = count
}
