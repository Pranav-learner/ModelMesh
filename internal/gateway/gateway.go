// Package gateway wires the Routing Engine and the Cache framework into a single
// request flow, giving the application a cache-transparent entry point:
//
//	Application -> gateway -> Router -> Cache Manager -> L1 -> L2 -> L3 -> Provider
//
// It is the orchestration layer (composition point) that later phases extend with
// additional middleware (circuit breaking, budget, shadow). Keeping it separate
// from the cache and routing packages leaves both of those reusable and decoupled.
//
// The gateway routes first (so the cache key includes the routed model), consults
// the multi-level cache (exact L1/L2, then semantic L3), and on a miss dispatches
// to the selected provider and populates every level. Provider dispatch on a miss
// is the only network work; cache population is best-effort and never fails a
// served response.
package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// Router is the narrow view of the Routing Engine the gateway needs.
// *routing.Manager satisfies it.
type Router interface {
	Select(ctx context.Context, rc routing.RoutingContext) (*routing.Selection, error)
}

// Cache is the narrow multi-level view the gateway needs. *cache.Manager
// satisfies it.
type Cache interface {
	Lookup(ctx context.Context, q cache.Query) (cache.Entry, bool, error)
	Store(ctx context.Context, q cache.Query, value []byte, ttl time.Duration) error
}

// CostEstimator returns the estimated USD cost of a request given its model and
// token usage, used to attribute cost savings to cache hits. It is optional; when
// nil, cost savings are reported as zero.
type CostEstimator func(model string, usage provider.Usage) float64

// Engine is the cache-aware routing gateway.
type Engine struct {
	router Router
	cache  Cache
	keys   cache.KeyGenerator
	cfg    cache.Config
	log    logger.Logger
	cost   CostEstimator

	// savings counters (token/cost saved by cache hits), tracked here because the
	// gateway is where cached responses are decoded.
	requests      atomic.Int64
	hits          atomic.Int64
	misses        atomic.Int64
	tokensSaved   atomic.Int64
	costSavedNano atomic.Int64 // cost saved in USD * 1e9, to keep an integer counter
}

// Option configures an Engine.
type Option func(*Engine)

// WithLogger injects a structured logger.
func WithLogger(l logger.Logger) Option {
	return func(e *Engine) {
		if l != nil {
			e.log = l
		}
	}
}

// WithKeyGenerator overrides the cache key generator.
func WithKeyGenerator(kg cache.KeyGenerator) Option {
	return func(e *Engine) {
		if kg != nil {
			e.keys = kg
		}
	}
}

// WithCostEstimator injects the cost estimator used for cost-saved statistics.
func WithCostEstimator(est CostEstimator) Option {
	return func(e *Engine) {
		if est != nil {
			e.cost = est
		}
	}
}

// New constructs a gateway Engine over a router and cache.
func New(router Router, c Cache, cfg cache.Config, opts ...Option) *Engine {
	e := &Engine{
		router: router,
		cache:  c,
		keys:   cache.NewKeyGenerator(),
		cfg:    cfg.WithDefaults(),
		log:    logger.Nop(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ChatResult is the outcome of a cached chat request.
type ChatResult struct {
	Response   provider.ChatResponse
	Cached     bool
	CacheLevel string
	Selection  *routing.Selection
}

// Chat resolves a chat request end to end: route to a provider/model, consult the
// multi-level cache, and on a miss dispatch and populate. The caller cannot tell
// whether the response came from cache or the provider except via the metadata.
func (e *Engine) Chat(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
	e.requests.Add(1)

	sel, err := e.router.Select(ctx, routing.ChatContext(req))
	if err != nil {
		return nil, err
	}

	if !e.cfg.Enabled {
		resp, err := sel.Provider.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		return &ChatResult{Response: resp, Cached: false, Selection: sel}, nil
	}

	q := cache.Query{
		Key:   e.keys.ChatKey(sel.Selected.Model, req),
		Model: sel.Selected.Model,
		Text:  renderPrompt(req.Messages),
	}

	if entry, found, gerr := e.cache.Lookup(ctx, q); gerr == nil && found {
		var resp provider.ChatResponse
		if err := json.Unmarshal(entry.Value, &resp); err == nil {
			e.recordHit(resp, sel.Selected.Model, entry.Level)
			return &ChatResult{Response: resp, Cached: true, CacheLevel: entry.Level, Selection: sel}, nil
		}
		e.log.Warn("failed to decode cached response; treating as miss")
	}

	e.misses.Add(1)
	resp, err := sel.Provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	// Best-effort population of every level: a cache write never fails the response.
	if value, merr := json.Marshal(resp); merr == nil {
		if serr := e.cache.Store(ctx, q, value, e.cfg.DefaultTTL); serr != nil {
			e.log.Warn("cache population failed", logger.Err(serr))
		}
	}

	return &ChatResult{Response: resp, Cached: false, Selection: sel}, nil
}

// recordHit updates savings counters when a response is served from cache.
func (e *Engine) recordHit(resp provider.ChatResponse, model, level string) {
	e.hits.Add(1)
	e.tokensSaved.Add(int64(resp.Usage.TotalTokens))
	if e.cost != nil {
		saved := e.cost(model, resp.Usage)
		e.costSavedNano.Add(int64(saved * 1e9))
	}
	e.log.Debug("cache hit",
		logger.String("level", level),
		logger.String("provider", resp.Provider),
		logger.String("model", resp.Model),
	)
}

// Stats is a snapshot of the gateway's cache-savings counters.
type Stats struct {
	Requests     int64   `json:"requests"`
	Hits         int64   `json:"hits"`
	Misses       int64   `json:"misses"`
	HitRatio     float64 `json:"hit_ratio"`
	TokensSaved  int64   `json:"tokens_saved"`
	CostSavedUSD float64 `json:"cost_saved_usd"`
}

// Stats returns the current gateway statistics.
func (e *Engine) Stats() Stats {
	hits := e.hits.Load()
	misses := e.misses.Load()
	var ratio float64
	if total := hits + misses; total > 0 {
		ratio = float64(hits) / float64(total)
	}
	return Stats{
		Requests:     e.requests.Load(),
		Hits:         hits,
		Misses:       misses,
		HitRatio:     ratio,
		TokensSaved:  e.tokensSaved.Load(),
		CostSavedUSD: float64(e.costSavedNano.Load()) / 1e9,
	}
}

// renderPrompt produces a canonical, single string of the conversation for
// semantic embedding. It is provider-agnostic and deterministic.
func renderPrompt(messages []provider.ChatMessage) string {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}
