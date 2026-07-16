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
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/symbiotes/modelmesh/internal/budget"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/metrics"
	"github.com/symbiotes/modelmesh/internal/optimization"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

// Request metadata keys through which the caller supplies budget identity to the
// optimization layer. Absent keys skip the budget stage.
const (
	MetaBudgetScope = "budget_scope" // "user" (default) or "team"
	MetaBudgetID    = "budget_id"    // the user/team identifier
)

// Router is the narrow view of the Routing Engine the gateway needs.
// *routing.Manager satisfies it. Select is used for simple (non-failover)
// dispatch; Route provides the ordered candidate list for failover.
type Router interface {
	Select(ctx context.Context, rc routing.RoutingContext) (*routing.Selection, error)
	Route(ctx context.Context, rc routing.RoutingContext) (routing.RoutingDecision, error)
}

// Cache is the narrow multi-level view the gateway needs. *cache.Manager
// satisfies it.
type Cache interface {
	Lookup(ctx context.Context, q cache.Query) (cache.Entry, bool, error)
	Store(ctx context.Context, q cache.Query, value []byte, ttl time.Duration) error
}

// ProviderResolver resolves a provider name to its LLMProvider, used by failover
// dispatch to reach each candidate. *provider.Manager satisfies it.
type ProviderResolver interface {
	GetProvider(name string) (provider.LLMProvider, error)
}

// CostEstimator returns the estimated USD cost of a request given its model and
// token usage, used to attribute cost savings to cache hits. It is optional; when
// nil, cost savings are reported as zero.
type CostEstimator func(model string, usage provider.Usage) float64

// Engine is the cache-aware routing gateway.
type Engine struct {
	router  Router
	cache   Cache
	keys    cache.KeyGenerator
	cfg     cache.Config
	log     logger.Logger
	cost    CostEstimator
	tracer  tracing.Tracer
	metrics metrics.Recorder

	// Optional resilience layer. When both are set, Chat dispatches with
	// breaker-guarded automatic failover across the routing candidate list.
	failover  *resilience.Failover
	providers ProviderResolver

	// Optional resource-optimization layer. When set, Chat runs the budget +
	// load-balancer pipeline before dispatch (see chatOptimized).
	optimizer *optimization.Optimizer

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

// WithFailover enables resilient dispatch: the gateway routes to the ordered
// candidate list and dispatches through the failover executor, skipping
// providers with open circuits and failing over on error. Both arguments are
// required to enable the mode.
func WithFailover(f *resilience.Failover, providers ProviderResolver) Option {
	return func(e *Engine) {
		if f != nil && providers != nil {
			e.failover = f
			e.providers = providers
		}
	}
}

// WithOptimizer enables the resource-optimization pipeline: before dispatch the
// gateway runs budget authorization (reject/downgrade) and load-balancer instance
// selection via the optimizer, then commits actual cost and latency afterward. A
// nil optimizer is ignored.
func WithOptimizer(o *optimization.Optimizer) Option {
	return func(e *Engine) {
		if o != nil {
			e.optimizer = o
		}
	}
}

// WithProviderResolver injects the resolver used to reach a provider by name.
// WithFailover already supplies one; this option lets the optimized dispatch path
// resolve providers without enabling failover. A nil resolver is ignored.
func WithProviderResolver(pr ProviderResolver) Option {
	return func(e *Engine) {
		if pr != nil {
			e.providers = pr
		}
	}
}

// WithTracer injects a distributed tracer. A nil tracer is ignored (the default
// is a no-op tracer).
func WithTracer(t tracing.Tracer) Option {
	return func(e *Engine) {
		if t != nil {
			e.tracer = t
		}
	}
}

// WithMetrics injects a metrics recorder. A nil recorder is ignored (the default
// is a no-op recorder).
func WithMetrics(rec metrics.Recorder) Option {
	return func(e *Engine) {
		if rec != nil {
			e.metrics = rec
		}
	}
}

// New constructs a gateway Engine over a router and cache.
func New(router Router, c Cache, cfg cache.Config, opts ...Option) *Engine {
	e := &Engine{
		router:  router,
		cache:   c,
		keys:    cache.NewKeyGenerator(),
		cfg:     cfg.WithDefaults(),
		log:     logger.Nop(),
		tracer:  tracing.Noop(),
		metrics: metrics.NoOp{},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ChatResult is the outcome of a cached chat request.
type ChatResult struct {
	Response provider.ChatResponse
	Cached   bool
	// CacheLevel is the cache layer that served the response ("l1"/"l2"/"l3"), or
	// empty on a miss.
	CacheLevel string
	// Similarity is the cosine similarity of a semantic (L3) hit; 0 otherwise.
	Similarity float64
	// Selection is set in simple (non-failover) dispatch mode.
	Selection *routing.Selection
	// Failover is set in failover dispatch mode, describing which candidates were
	// skipped/failed and which served the request.
	Failover *resilience.FailoverOutcome
	// Optimization is set in optimized dispatch mode, carrying the budget verdict,
	// any downgrade, and the chosen provider instance.
	Optimization *optimization.Plan
}

// Chat resolves a chat request end to end: route to a provider/model, consult the
// multi-level cache, and on a miss dispatch and populate. When resilient dispatch
// is enabled (WithFailover) it dispatches through the failover executor; otherwise
// it uses the single selected provider. The caller cannot tell whether the
// response came from cache or the provider except via the metadata.
func (e *Engine) Chat(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
	e.requests.Add(1)

	// Correlation + root span: every request gets a request ID and a trace.
	ctx, requestID := tracing.EnsureRequestID(ctx)
	ctx, span := e.tracer.Start(ctx, tracing.SpanRequest, tracing.String("request_id", requestID))
	defer span.End()
	start := time.Now()

	var res *ChatResult
	var err error
	switch {
	case e.optimizer != nil:
		res, err = e.chatOptimized(ctx, req)
	case e.failover != nil:
		res, err = e.chatWithFailover(ctx, req)
	default:
		res, err = e.chatSimple(ctx, req)
	}

	log := tracing.LoggerWith(ctx, e.log)
	duration := time.Since(start)
	e.metrics.GatewayRequest(err == nil, duration)
	if err != nil {
		span.RecordError(err)
		log.Error("request failed", logger.Err(err), logger.String("latency", duration.String()))
		return nil, err
	}

	span.SetAttributes(
		tracing.String("provider", res.Response.Provider),
		tracing.String("model", res.Response.Model),
		tracing.Bool("cached", res.Cached),
		tracing.String("cache_level", res.CacheLevel),
	)
	fields := []logger.Field{
		logger.String("provider", res.Response.Provider),
		logger.String("model", res.Response.Model),
		logger.Bool("cached", res.Cached),
		logger.String("cache_level", res.CacheLevel),
		logger.String("latency", duration.String()),
	}
	if res.Selection != nil {
		fields = append(fields, logger.Any("score", res.Selection.Selected.Score))
	}
	if res.Failover != nil {
		fields = append(fields, logger.Bool("failover", res.Failover.FailoverUsed))
	}
	log.Info("request completed", fields...)
	return res, nil
}

// chatSimple dispatches to the single selected provider (no failover).
func (e *Engine) chatSimple(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
	rstart := time.Now()
	rctx, rspan := e.tracer.Start(ctx, tracing.SpanRoute)
	sel, err := e.router.Select(rctx, e.routingContext(ctx, req))
	if err != nil {
		rspan.RecordError(err)
		rspan.End()
		return nil, err
	}
	rspan.SetAttributes(
		tracing.String("strategy", sel.Decision.Strategy),
		tracing.String("provider", sel.Selected.Provider),
		tracing.String("model", sel.Selected.Model),
		tracing.Float("score", sel.Selected.Score),
	)
	rspan.End()
	e.metrics.RoutingDecision(sel.Selected.Provider, time.Since(rstart))

	if !e.cfg.Enabled {
		resp, err := e.dispatch(ctx, sel.Provider, sel.Selected.Provider, sel.Selected.Model, req)
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

	if entry, found := e.lookup(ctx, q); found {
		var resp provider.ChatResponse
		if err := json.Unmarshal(entry.Value, &resp); err == nil {
			e.recordHit(resp, sel.Selected.Model, entry.Level)
			return &ChatResult{
				Response:   resp,
				Cached:     true,
				CacheLevel: entry.Level,
				Similarity: entry.Similarity,
				Selection:  sel,
			}, nil
		}
		e.log.Warn("failed to decode cached response; treating as miss")
	}

	e.misses.Add(1)
	e.metrics.CacheMiss()
	resp, err := e.dispatch(ctx, sel.Provider, sel.Selected.Provider, sel.Selected.Model, req)
	if err != nil {
		return nil, err
	}
	e.populate(ctx, q, resp)
	return &ChatResult{Response: resp, Cached: false, Selection: sel}, nil
}

// chatOptimized runs the resource-optimization pipeline before dispatch: it
// resolves provider/model/instance (with budget-driven downgrade) via the
// optimizer, serves from cache when possible, dispatches to the chosen instance,
// and commits actual cost + latency. A budget rejection returns
// optimization.ErrBudgetExceeded and never dispatches.
func (e *Engine) chatOptimized(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
	requestID, _ := tracing.RequestIDFromContext(ctx)
	scope, id := budgetIdentity(req)

	ostart := time.Now()
	octx, ospan := e.tracer.Start(ctx, tracing.SpanRoute)
	plan, err := e.optimizer.Optimize(octx, optimization.OptimizeRequest{
		Chat:      req,
		Scope:     scope,
		BudgetID:  id,
		RequestID: requestID,
	})
	if err != nil {
		ospan.RecordError(err)
		ospan.End()
		return nil, err
	}
	ospan.SetAttributes(
		tracing.String("provider", plan.Provider),
		tracing.String("model", plan.Model),
		tracing.String("instance", plan.InstanceID()),
		tracing.Bool("downgraded", plan.Downgraded),
	)
	ospan.End()
	e.metrics.RoutingDecision(plan.Provider, time.Since(ostart))

	if !plan.Allowed() {
		return nil, optimization.ErrBudgetExceeded
	}

	model := plan.Model
	q := cache.Query{Key: e.keys.ChatKey(model, req), Model: model, Text: renderPrompt(req.Messages)}

	if e.cfg.Enabled {
		if entry, found := e.lookup(ctx, q); found {
			var resp provider.ChatResponse
			if derr := json.Unmarshal(entry.Value, &resp); derr == nil {
				e.recordHit(resp, model, entry.Level)
				return &ChatResult{Response: resp, Cached: true, CacheLevel: entry.Level, Similarity: entry.Similarity, Optimization: plan}, nil
			}
			e.log.Warn("failed to decode cached response; treating as miss")
		}
		e.misses.Add(1)
		e.metrics.CacheMiss()
	}

	p, err := e.resolveProvider(plan)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	resp, derr := e.dispatch(ctx, p, plan.Provider, model, withModel(req, model))
	latency := time.Since(start)
	// Commit closes the loop: actual cost to the budget (on success), observed
	// latency to the load balancer. It never fails the produced response.
	if cerr := e.optimizer.Commit(ctx, plan, resp.Usage, latency, derr == nil); cerr != nil {
		e.log.Warn("optimizer commit failed", logger.Err(cerr))
	}
	if derr != nil {
		return nil, derr
	}

	if e.cfg.Enabled {
		e.populate(ctx, q, resp)
	}
	return &ChatResult{Response: resp, Cached: false, Optimization: plan}, nil
}

// resolveProvider returns the concrete provider for a plan: the chosen instance's
// client when present, otherwise the named provider via the resolver.
func (e *Engine) resolveProvider(plan *optimization.Plan) (provider.LLMProvider, error) {
	if c := plan.Client(); c != nil {
		return c, nil
	}
	if e.providers == nil {
		return nil, fmt.Errorf("gateway: no provider resolver for %q (wire WithProviderResolver or instance clients)", plan.Provider)
	}
	return e.providers.GetProvider(plan.Provider)
}

// budgetIdentity extracts the budget scope and ID from request metadata.
func budgetIdentity(req provider.ChatRequest) (budget.Scope, string) {
	scope := budget.ScopeUser
	if req.Metadata[MetaBudgetScope] == string(budget.ScopeTeam) {
		scope = budget.ScopeTeam
	}
	return scope, req.Metadata[MetaBudgetID]
}

// routingContext builds a routing context carrying the correlation ID from ctx.
func (e *Engine) routingContext(ctx context.Context, req provider.ChatRequest) routing.RoutingContext {
	rc := routing.ChatContext(req)
	if id, ok := tracing.RequestIDFromContext(ctx); ok {
		rc.RequestID = id
	}
	return rc
}

// lookup performs a traced cache lookup, returning the entry and whether it hit.
func (e *Engine) lookup(ctx context.Context, q cache.Query) (cache.Entry, bool) {
	cctx, cspan := e.tracer.Start(ctx, tracing.SpanCacheLookup)
	defer cspan.End()
	entry, found, err := e.cache.Lookup(cctx, q)
	hit := err == nil && found
	cspan.SetAttributes(tracing.Bool("hit", hit))
	if hit {
		cspan.SetAttributes(tracing.String("level", entry.Level), tracing.Float("similarity", entry.Similarity))
	}
	return entry, hit
}

// populate best-effort stores the response; a cache write never fails a response.
func (e *Engine) populate(ctx context.Context, q cache.Query, resp provider.ChatResponse) {
	value, merr := json.Marshal(resp)
	if merr != nil {
		return
	}
	if serr := e.cache.Store(ctx, q, value, e.cfg.DefaultTTL); serr != nil {
		e.log.Warn("cache population failed", logger.Err(serr))
	}
}

// dispatch performs a traced, metered provider call.
func (e *Engine) dispatch(ctx context.Context, p provider.LLMProvider, providerName, model string, req provider.ChatRequest) (provider.ChatResponse, error) {
	pctx, pspan := e.tracer.Start(ctx, tracing.SpanProviderCall,
		tracing.String("provider", providerName), tracing.String("model", model))
	defer pspan.End()
	start := time.Now()
	resp, err := p.Chat(pctx, req)
	e.metrics.ProviderRequest(providerName, err == nil, time.Since(start))
	if err != nil {
		pspan.RecordError(err)
		return provider.ChatResponse{}, err
	}
	pspan.SetStatus(true, "")
	return resp, nil
}

// chatWithFailover dispatches with breaker-guarded automatic failover across the
// routing candidate list. It routes first (so the cache key uses the intended,
// top-ranked model), consults the cache, and on a miss tries candidates in rank
// order through the failover executor: providers with open circuits are skipped,
// and a failing provider fails over to the next. The response is cached under the
// intended key regardless of which provider ultimately served it.
func (e *Engine) chatWithFailover(ctx context.Context, req provider.ChatRequest) (*ChatResult, error) {
	rstart := time.Now()
	rctx, rspan := e.tracer.Start(ctx, tracing.SpanRoute)
	decision, err := e.router.Route(rctx, e.routingContext(ctx, req))
	if err != nil {
		rspan.RecordError(err)
		rspan.End()
		return nil, err
	}
	rspan.SetAttributes(
		tracing.String("strategy", decision.Strategy),
		tracing.String("provider", decision.Selected.Provider),
		tracing.String("model", decision.Selected.Model),
		tracing.Int("candidates", len(decision.Candidates)),
	)
	rspan.End()
	e.metrics.RoutingDecision(decision.Selected.Provider, time.Since(rstart))

	topModel := decision.Selected.Model
	q := cache.Query{Key: e.keys.ChatKey(topModel, req), Model: topModel, Text: renderPrompt(req.Messages)}

	if e.cfg.Enabled {
		if entry, found := e.lookup(ctx, q); found {
			var resp provider.ChatResponse
			if err := json.Unmarshal(entry.Value, &resp); err == nil {
				e.recordHit(resp, topModel, entry.Level)
				return &ChatResult{Response: resp, Cached: true, CacheLevel: entry.Level, Similarity: entry.Similarity}, nil
			}
			e.log.Warn("failed to decode cached response; treating as miss")
		}
		e.misses.Add(1)
		e.metrics.CacheMiss()
	}

	dctx, dspan := e.tracer.Start(ctx, tracing.SpanDispatch)
	var resp provider.ChatResponse
	outcome, err := e.failover.Do(dctx, toTargets(decision.Candidates), func(cctx context.Context, tg resilience.Target) error {
		p, gerr := e.providers.GetProvider(tg.Provider)
		if gerr != nil {
			return gerr
		}
		r, cerr := e.dispatch(cctx, p, tg.Provider, tg.Model, withModel(req, tg.Model))
		if cerr != nil {
			return cerr
		}
		resp = r
		return nil
	})
	if err != nil {
		dspan.RecordError(err)
		dspan.End()
		return nil, err
	}
	dspan.SetAttributes(
		tracing.String("served", outcome.Served.Provider),
		tracing.Bool("failover", outcome.FailoverUsed),
		tracing.Int("attempts", len(outcome.Attempts)),
	)
	dspan.End()
	if outcome.FailoverUsed {
		e.metrics.Failover()
	}

	if e.cfg.Enabled {
		e.populate(ctx, q, resp)
	}
	return &ChatResult{Response: resp, Cached: false, Failover: &outcome}, nil
}

// toTargets converts routing candidates to failover targets.
func toTargets(candidates []routing.Candidate) []resilience.Target {
	out := make([]resilience.Target, len(candidates))
	for i, c := range candidates {
		out[i] = resilience.Target{Provider: c.Provider, Model: c.Model}
	}
	return out
}

// withModel returns a copy of req with the model set (req is passed by value; the
// Messages slice is shared but never mutated).
func withModel(req provider.ChatRequest, model string) provider.ChatRequest {
	req.Model = model
	return req
}

// recordHit updates savings counters and metrics when a response is served from
// cache.
func (e *Engine) recordHit(resp provider.ChatResponse, model, level string) {
	e.hits.Add(1)
	e.tokensSaved.Add(int64(resp.Usage.TotalTokens))
	e.metrics.CacheHit(level)
	e.metrics.AddTokensSaved(resp.Usage.TotalTokens)
	if e.cost != nil {
		saved := e.cost(model, resp.Usage)
		e.costSavedNano.Add(int64(saved * 1e9))
		e.metrics.AddCostSaved(saved)
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
