package analysis

import (
	"regexp"
	"strings"
)

// codeKeywordRe matches common source-code keywords across popular languages.
var codeKeywordRe = regexp.MustCompile(`\b(func|def|class|function|const|let|var|import|package|public|private|static|void|return|struct|interface|#include|println|System\.out|console\.log|print\(|SELECT|INSERT|UPDATE|DELETE|FROM|WHERE)\b`)

// codeSymbols are characters whose density signals code.
const codeSymbols = "{}[]();=<>"

// CodeExtractor detects whether a prompt contains source code. It combines three
// signals — fenced code blocks, language keywords, and code-symbol density /
// indentation — so a fenced block alone, or keywords plus structure, both trip it,
// while prose stays below the threshold. No language parsing or ML is involved.
type CodeExtractor struct{}

// Name returns the extractor identifier.
func (CodeExtractor) Name() string { return "code" }

// Extract sets HasCode.
func (CodeExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	text := p.Text
	if strings.Contains(text, "```") {
		f.HasCode = true
		return
	}
	keyword := codeKeywordRe.MatchString(text)
	dense := symbolDensity(text) > 0.03
	indented := indentedLines(text) >= 2
	f.HasCode = (keyword && (dense || indented)) || (dense && indented)
}

// symbolDensity is the fraction of characters that are code symbols.
func symbolDensity(s string) float64 {
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range s {
		if strings.ContainsRune(codeSymbols, r) {
			n++
		}
	}
	return float64(n) / float64(len(s))
}

// indentedLines counts lines that begin with indentation (a tab or two+ spaces).
func indentedLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "  ") {
			n++
		}
	}
	return n
}
