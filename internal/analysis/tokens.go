package analysis

import (
	"unicode/utf8"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Token-estimation defaults.
const (
	// DefaultCharsPerToken is the average characters-per-token ratio used by the
	// heuristic estimator (~4 chars/token is typical for English + code).
	DefaultCharsPerToken = 4.0
	// DefaultExpectedOutputTokens is used when a request does not cap output via
	// MaxTokens.
	DefaultExpectedOutputTokens = 300
)

// TokenEstimator produces a lightweight token estimate for a request. It is an
// interface so a future phase can replace the heuristic with a real tokenizer
// without changing the engine or its callers.
type TokenEstimator interface {
	Estimate(p Preprocessed, req provider.ChatRequest) TokenEstimate
}

// HeuristicEstimator estimates tokens from character count using a fixed
// characters-per-token ratio. Input tokens come from the normalized context;
// expected output tokens come from the request's MaxTokens when set, else a
// default. It is deterministic and dependency-free.
type HeuristicEstimator struct {
	charsPerToken       float64
	defaultOutputTokens int
}

// Compile-time assertion.
var _ TokenEstimator = HeuristicEstimator{}

// EstimatorOption configures a HeuristicEstimator.
type EstimatorOption func(*HeuristicEstimator)

// WithCharsPerToken overrides the characters-per-token ratio.
func WithCharsPerToken(r float64) EstimatorOption {
	return func(e *HeuristicEstimator) {
		if r > 0 {
			e.charsPerToken = r
		}
	}
}

// WithDefaultOutputTokens overrides the expected-output default.
func WithDefaultOutputTokens(n int) EstimatorOption {
	return func(e *HeuristicEstimator) {
		if n > 0 {
			e.defaultOutputTokens = n
		}
	}
}

// NewHeuristicEstimator constructs a HeuristicEstimator with defaults applied.
func NewHeuristicEstimator(opts ...EstimatorOption) HeuristicEstimator {
	e := HeuristicEstimator{charsPerToken: DefaultCharsPerToken, defaultOutputTokens: DefaultExpectedOutputTokens}
	for _, opt := range opts {
		opt(&e)
	}
	return e
}

// Estimate computes the input/output/total token estimate.
func (e HeuristicEstimator) Estimate(p Preprocessed, req provider.ChatRequest) TokenEstimate {
	input := tokensFromChars(utf8.RuneCountInString(p.Text), e.charsPerToken)
	output := e.defaultOutputTokens
	if req.MaxTokens > 0 {
		output = req.MaxTokens
	}
	return TokenEstimate{
		InputTokens:          input,
		ExpectedOutputTokens: output,
		EstimatedTotalTokens: input + output,
	}
}

// tokensFromChars converts a character count to a token count, rounding up so a
// non-empty prompt never estimates zero tokens.
func tokensFromChars(chars int, ratio float64) int {
	if chars <= 0 {
		return 0
	}
	n := int(float64(chars)/ratio + 0.999)
	if n < 1 {
		n = 1
	}
	return n
}
