// Package gateway wires the Routing Engine and the Cache framework into a single
// request flow, giving the application a cache-transparent entry point:
//
//	Application -> gateway -> Router -> Cache Manager -> L1 (memory) -> Provider
//
// It is the orchestration layer (composition point) that later phases extend with
// additional middleware (circuit breaking, budget, shadow). Keeping it separate
// from the cache and routing packages leaves both of those reusable and decoupled.
//
// In Phase 3 Part 1 the gateway routes first (so the cache key includes the routed
// model), consults the cache, and on a miss dispatches to the selected provider
// and populates the cache. Provider dispatch on a miss is the only network work;
// cache population is best-effort and never fails a served response.
package gateway

import (
	"context"
	"encoding/json"
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

// CacheReadWriter is the narrow view of the cache the gateway needs.
// *cache.Manager satisfies it.
type CacheReadWriter interface {
	Get(ctx context.Context, key string) (cache.Entry, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// Engine is the cache-aware routing gateway.
type Engine struct {
	router Router
	cache  CacheReadWriter
	keys   cache.KeyGenerator
	cfg    cache.Config
	log    logger.Logger
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

// New constructs a gateway Engine over a router and cache with the given cache
// configuration.
func New(router Router, c CacheReadWriter, cfg cache.Config, opts ...Option) *Engine {
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

// ChatResult is the outcome of a cached chat request: the normalized response,
// whether it was served from cache (and from which level), and the routing
// selection that produced it.
type ChatResult struct {
	Response   provider.ChatResponse
	Cached     bool
	CacheLevel string
	Selection  *routing.Selection
}

// Chat resolves a chat request end to end: route to a provider/model, consult the
// cache, and on a miss dispatch and populate. The caller cannot tell whether the
// response came from cache or the provider except via the result metadata.
func (e *Engine) Chat(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
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

	key := e.keys.ChatKey(sel.Selected.Model, req)

	if entry, found, gerr := e.cache.Get(ctx, key); gerr == nil && found {
		var resp provider.ChatResponse
		if err := json.Unmarshal(entry.Value, &resp); err == nil {
			e.log.Debug("cache hit",
				logger.String("level", entry.Level),
				logger.String("provider", resp.Provider),
				logger.String("model", resp.Model),
			)
			return &ChatResult{Response: resp, Cached: true, CacheLevel: entry.Level, Selection: sel}, nil
		}
		// A corrupt cached value degrades to a miss; fall through to dispatch.
		e.log.Warn("failed to decode cached response; treating as miss")
	}

	resp, err := sel.Provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	// Best-effort population: a cache write failure never fails the response.
	if value, merr := json.Marshal(resp); merr == nil {
		if serr := e.cache.Set(ctx, key, value, e.cfg.DefaultTTL); serr != nil {
			e.log.Warn("cache population failed", logger.Err(serr))
		}
	}

	return &ChatResult{Response: resp, Cached: false, Selection: sel}, nil
}
