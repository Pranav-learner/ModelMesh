package analysis

import "regexp"

var (
	// latexCmdRe matches common LaTeX math commands.
	latexCmdRe = regexp.MustCompile(`\\(frac|sum|int|sqrt|prod|lim|infty|partial|nabla|alpha|beta|gamma|theta|lambda|sigma|pi|cdot|times|div|leq|geq|neq|approx|equiv|begin\{(equation|align|matrix|bmatrix|pmatrix)\})`)
	// arithRe matches arithmetic between numbers or a variable raised to a power.
	arithRe = regexp.MustCompile(`\d\s*[+\-*/^]\s*\d|[a-zA-Z]\^\d|\bsqrt\(`)
	// mathKeywordRe matches mathematical vocabulary.
	mathKeywordRe = regexp.MustCompile(`(?i)\b(equation|integral|derivative|theorem|matrix|matrices|polynomial|quadratic|algebra|calculus|logarithm|factorial|eigenvalue|summation|probability|coefficient)\b`)
)

// mathSymbols are Unicode characters that indicate mathematical content.
const mathSymbols = "∑∫√≤≥≠±×÷π∞∂∇∈∀∃∏≈≡"

// MathExtractor detects mathematical content from LaTeX commands, Unicode math
// symbols, arithmetic expressions, and mathematical vocabulary. Purely heuristic.
type MathExtractor struct{}

// Name returns the extractor identifier.
func (MathExtractor) Name() string { return "math" }

// Extract sets HasMath.
func (MathExtractor) Extract(p Preprocessed, f *PromptFeatures) {
	text := p.Text
	f.HasMath = latexCmdRe.MatchString(text) ||
		containsAnyRune(text, mathSymbols) ||
		mathKeywordRe.MatchString(text) ||
		arithRe.MatchString(text)
}

func containsAnyRune(s, set string) bool {
	for _, r := range s {
		for _, t := range set {
			if r == t {
				return true
			}
		}
	}
	return false
}
