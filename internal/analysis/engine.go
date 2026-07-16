package analysis

import (
	"context"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

// DefaultLongContextTokens is the input-token threshold above which a request is
// flagged as long-context in the routing hints.
const DefaultLongContextTokens = 4000

// Analyzer is the analysis contract exposed to the request pipeline. *Engine
// implements it; the gateway depends on this interface, not the concrete engine.
type Analyzer interface {
	// Analyze produces the structured analysis of a request. It never fails: an
	// empty request yields a zero-valued-but-valid result.
	Analyze(ctx context.Context, req provider.ChatRequest) AnalysisResult
}

// Compile-time assertion.
var _ Analyzer = (*Engine)(nil)

// Engine is the centralized request-analysis engine. It runs the pipeline —
// preprocess, extract features, estimate tokens, derive routing hints — and is
// the single place the pipeline's stages are composed.
type Engine struct {
	pre        *Preprocessor
	extractors []Extractor
	tokens     TokenEstimator
	classifier Classifier
	hints      HintGenerator
	log        logger.Logger
	longCtx    int
}

// Option configures an Engine.
type Option func(*Engine)

// WithPreprocessor overrides the preprocessor.
func WithPreprocessor(p *Preprocessor) Option {
	return func(e *Engine) {
		if p != nil {
			e.pre = p
		}
	}
}

// WithExtractors replaces the extractor set (order preserved).
func WithExtractors(extractors ...Extractor) Option {
	return func(e *Engine) {
		if len(extractors) > 0 {
			e.extractors = extractors
		}
	}
}

// WithExtractor appends a single extractor to the set — the seam for adding a new
// feature without disturbing the defaults.
func WithExtractor(x Extractor) Option {
	return func(e *Engine) {
		if x != nil {
			e.extractors = append(e.extractors, x)
		}
	}
}

// WithTokenEstimator overrides the token estimator.
func WithTokenEstimator(t TokenEstimator) Option {
	return func(e *Engine) {
		if t != nil {
			e.tokens = t
		}
	}
}

// WithClassifier overrides the complexity classifier.
func WithClassifier(c Classifier) Option {
	return func(e *Engine) {
		if c != nil {
			e.classifier = c
		}
	}
}

// WithClassifierConfig sets the rule classifier from configuration (a convenience
// over WithClassifier + NewRuleClassifier).
func WithClassifierConfig(cfg ClassifierConfig) Option {
	return func(e *Engine) {
		e.classifier = NewRuleClassifier(cfg)
	}
}

// WithHintGenerator overrides the routing-hint generator.
func WithHintGenerator(h HintGenerator) Option {
	return func(e *Engine) {
		if h != nil {
			e.hints = h
		}
	}
}

// WithHintConfig sets the rule hint generator from configuration.
func WithHintConfig(cfg HintConfig) Option {
	return func(e *Engine) {
		e.hints = NewHintGenerator(cfg)
	}
}

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) Option {
	return func(e *Engine) {
		if l != nil {
			e.log = l
		}
	}
}

// WithLongContextThreshold sets the input-token threshold for the long-context
// hint.
func WithLongContextThreshold(tokens int) Option {
	return func(e *Engine) {
		if tokens > 0 {
			e.longCtx = tokens
		}
	}
}

// New constructs an analysis Engine with the default preprocessor, extractor set,
// and heuristic token estimator, overridable via options.
func New(opts ...Option) *Engine {
	e := &Engine{
		pre:        NewPreprocessor(),
		extractors: DefaultExtractors(),
		tokens:     NewHeuristicEstimator(),
		classifier: NewRuleClassifier(DefaultClassifierConfig()),
		hints:      NewHintGenerator(DefaultHintConfig()),
		log:        logger.Nop(),
		longCtx:    DefaultLongContextTokens,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Analyze runs the full analysis pipeline for a request.
func (e *Engine) Analyze(_ context.Context, req provider.ChatRequest) AnalysisResult {
	pre := e.pre.Process(req)

	var features PromptFeatures
	for _, x := range e.extractors {
		x.Extract(pre, &features)
	}

	tokens := e.tokens.Estimate(pre, req)
	features.EstimatedContextSize = tokens.InputTokens

	// Base routing hints (Part 1), then complexity classification and the
	// classification-driven hints (Part 2).
	hints := e.buildHints(features, tokens)
	signals := signalsFrom(features, tokens, e.longCtx)
	classification := e.classifier.Classify(signals)
	classification.HintReasons = e.hints.Generate(signals, classification, &hints)

	e.log.Debug("request analyzed",
		logger.Int("messages", features.MessageCount),
		logger.Int("input_tokens", tokens.InputTokens),
		logger.String("complexity", classification.Complexity.String()),
		logger.String("tier", string(hints.PreferredModelTier)),
		logger.Int("triggered_rules", len(classification.TriggeredRules)),
	)

	return AnalysisResult{
		Preprocessed:   pre,
		Features:       features,
		Tokens:         tokens,
		Classification: classification,
		Hints:          hints,
	}
}

// buildHints distills the features and token estimate into the routing hints.
func (e *Engine) buildHints(f PromptFeatures, t TokenEstimate) RoutingHints {
	return RoutingHints{
		EstimatedInputTokens:  t.InputTokens,
		EstimatedOutputTokens: t.ExpectedOutputTokens,
		HasCode:               f.HasCode,
		HasMath:               f.HasMath,
		HasStructuredData:     f.HasStructuredData,
		ConversationTurns:     f.MessageCount,
		LongContext:           t.InputTokens >= e.longCtx,
		MultiTurn:             f.ConversationHistoryLength > 0,
	}
}
