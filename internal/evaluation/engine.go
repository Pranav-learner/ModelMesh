package evaluation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// Engine compares primary/shadow response pairs and stores the results. It
// implements shadow.Evaluator, so it can be wired directly into the shadow
// Manager, and also exposes a pure Compare for use without the shadow package.
type Engine struct {
	store    *store
	textSim  TextSimilarity
	embedSim EmbeddingSimilarity
	cost     CostModel
	log      logger.Logger
	clock    func() time.Time
	idgen    func() string
}

// Compile-time assertion that Engine satisfies the shadow evaluator seam.
var _ shadow.Evaluator = (*Engine)(nil)

// Option configures an Engine.
type Option func(*Engine)

// WithTextSimilarity overrides the deterministic text-similarity function.
func WithTextSimilarity(fn TextSimilarity) Option {
	return func(e *Engine) {
		if fn != nil {
			e.textSim = fn
		}
	}
}

// WithEmbeddingSimilarity plugs in the optional embedding-similarity abstraction.
func WithEmbeddingSimilarity(fn EmbeddingSimilarity) Option {
	return func(e *Engine) {
		if fn != nil {
			e.embedSim = fn
		}
	}
}

// WithCostModel injects the cost model used to price token usage.
func WithCostModel(cm CostModel) Option {
	return func(e *Engine) {
		if cm != nil {
			e.cost = cm
		}
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

// WithClock injects a time source, for deterministic timestamps in tests.
func WithClock(now func() time.Time) Option {
	return func(e *Engine) {
		if now != nil {
			e.clock = now
		}
	}
}

// WithIDGenerator overrides the record ID generator.
func WithIDGenerator(gen func() string) Option {
	return func(e *Engine) {
		if gen != nil {
			e.idgen = gen
		}
	}
}

// WithMaxRecords bounds the retained record history.
func WithMaxRecords(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.store = newStore(n)
		}
	}
}

// New constructs an evaluation Engine with the default word-cosine similarity and
// zero-cost model, overridable via options.
func New(opts ...Option) *Engine {
	e := &Engine{
		store:   newStore(DefaultMaxRecords),
		textSim: wordCosine,
		cost:    zeroCost{},
		log:     logger.Nop(),
		clock:   time.Now,
		idgen:   newID,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Evaluate consumes a completed shadow comparison, compares the pair (when the
// shadow succeeded), stores the record, and returns it. It satisfies
// shadow.Evaluator. It never blocks the caller beyond the comparison itself.
func (e *Engine) Evaluate(_ context.Context, c shadow.Comparison) {
	e.record(c)
}

// record builds and stores the EvaluationRecord for a comparison.
func (e *Engine) record(c shadow.Comparison) EvaluationRecord {
	rec := EvaluationRecord{
		ID:            e.idgen(),
		CorrelationID: c.CorrelationID,
		Timestamp:     e.clock(),
	}
	if !c.ShadowResult.Success {
		rec.Comparable = false
		rec.ShadowError = c.ShadowResult.Err
		rec.Comparison = ComparisonResult{
			PrimaryProvider: c.Primary.Provider, PrimaryModel: c.Primary.Model,
			ShadowProvider: c.Shadow.Provider, ShadowModel: c.Shadow.Model,
		}
		e.store.add(rec)
		return rec
	}

	rec.Comparable = true
	rec.Comparison = e.Compare(
		Side{Provider: c.Primary.Provider, Model: c.Primary.Model, Response: c.PrimaryResponse, Latency: c.PrimaryLatency},
		Side{Provider: c.Shadow.Provider, Model: c.Shadow.Model, Response: c.ShadowResult.Response, Latency: c.ShadowResult.Latency},
	)
	e.store.add(rec)
	e.log.Debug("shadow evaluated",
		logger.String("primary", c.Primary.Provider),
		logger.String("shadow", c.Shadow.Provider),
		logger.String("winner", string(rec.Comparison.Winner)),
	)
	return rec
}

// Compare produces the full comparison of a primary/shadow pair. It is pure and
// deterministic — the same inputs always yield the same result.
func (e *Engine) Compare(primary, shadow Side) ComparisonResult {
	pText := responseText(primary.Response)
	sText := responseText(shadow.Response)

	// Price and label by the model that actually served the response (adapters
	// report it), falling back to the requested model on the side.
	pModel := servedModel(primary)
	sModel := servedModel(shadow)

	quality := QualityMetrics{
		ExactMatch:          exactMatch(pText, sText),
		TextSimilarity:      round4(e.textSim(pText, sText)),
		PrimaryLength:       len(pText),
		ShadowLength:        len(sText),
		LengthDifference:    len(sText) - len(pText),
		PrimaryFinishReason: finishReason(primary.Response),
		ShadowFinishReason:  finishReason(shadow.Response),
	}
	quality.FinishReasonMatch = quality.PrimaryFinishReason == quality.ShadowFinishReason
	if e.embedSim != nil {
		if score, ok := e.embedSim(pText, sText); ok {
			quality.EmbeddingSimilarity = round4(score)
			quality.HasEmbedding = true
		}
	}

	latency := LatencyMetrics{
		PrimaryLatency: primary.Latency,
		ShadowLatency:  shadow.Latency,
		Difference:     shadow.Latency - primary.Latency,
		ShadowFaster:   shadow.Latency < primary.Latency,
	}

	pCost := e.cost.Cost(pModel, primary.Response.Usage)
	sCost := e.cost.Cost(sModel, shadow.Response.Usage)
	cost := CostMetrics{
		PrimaryCost:     pCost,
		ShadowCost:      sCost,
		Difference:      sCost - pCost,
		ShadowCheaper:   sCost < pCost,
		PrimaryTokens:   primary.Response.Usage.TotalTokens,
		ShadowTokens:    shadow.Response.Usage.TotalTokens,
		TokenDifference: shadow.Response.Usage.TotalTokens - primary.Response.Usage.TotalTokens,
	}

	return ComparisonResult{
		PrimaryProvider: primary.Provider, PrimaryModel: pModel,
		ShadowProvider: shadow.Provider, ShadowModel: sModel,
		Quality: quality, Latency: latency, Cost: cost,
		Winner: efficiencyWinner(cost, latency),
	}
}

// servedModel returns the model that actually served a response (the adapter
// reports it), falling back to the requested model on the side.
func servedModel(s Side) string {
	if s.Response.Model != "" {
		return s.Response.Model
	}
	return s.Model
}

// efficiencyWinner scores the two sides on cost and latency (one point each) and
// returns the leader, or a tie. Quality is not a winner axis — similarity measures
// agreement, not correctness, so it is reported but never decides a winner.
func efficiencyWinner(cost CostMetrics, latency LatencyMetrics) Winner {
	primary, shadow := 0, 0
	switch {
	case cost.ShadowCheaper:
		shadow++
	case cost.PrimaryCost < cost.ShadowCost:
		primary++
	}
	switch {
	case latency.ShadowFaster:
		shadow++
	case latency.PrimaryLatency < latency.ShadowLatency:
		primary++
	}
	switch {
	case shadow > primary:
		return WinnerShadow
	case primary > shadow:
		return WinnerPrimary
	default:
		return WinnerTie
	}
}

// Records returns a copy of the stored evaluation records.
func (e *Engine) Records() []EvaluationRecord { return e.store.all() }

// responseText concatenates the assistant message content of a response.
func responseText(resp provider.ChatResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	// Use the first choice's content; responses are single-choice on this path.
	return resp.Choices[0].Message.Content
}

// finishReason returns the first choice's finish reason.
func finishReason(resp provider.ChatResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return string(resp.Choices[0].FinishReason)
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "eval_0000000000000000"
	}
	return "eval_" + hex.EncodeToString(b[:])
}
