package gateway_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// countingProvider wraps a mock provider, counting Chat dispatches so tests can
// assert the cache prevented a provider call.
func countingProvider(name string, calls *int32) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			atomic.AddInt32(calls, 1)
			return provider.ChatResponse{
				ID:       "resp",
				Provider: name,
				Model:    "mock-chat",
				Choices: []provider.Choice{{
					Index:        0,
					Message:      provider.ChatMessage{Role: provider.RoleAssistant, Content: "hello from " + name},
					FinishReason: provider.FinishReasonStop,
				}},
				Usage: provider.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
			}, nil
		}),
	)
}

func newGateway(t *testing.T, cfg cache.Config, calls *int32) *gateway.Engine {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register(countingProvider("openai", calls)); err != nil {
		t.Fatalf("register: %v", err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))

	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatalf("routing.Build: %v", err)
	}

	l1 := cache.NewMemoryCache(cfg.WithDefaults().Memory)
	t.Cleanup(func() { _ = l1.Close() })
	cm := cache.NewManager([]cache.Cache{l1})

	return gateway.New(router, cm, cfg)
}

func chatReq(content string) provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: content}}}
}

func TestGateway_CacheMissThenHit(t *testing.T) {
	var calls int32
	e := newGateway(t, cache.DefaultConfig(), &calls)
	ctx := context.Background()
	req := chatReq("hello world")

	// First call: miss -> dispatch -> populate.
	r1, err := e.Chat(ctx, req)
	if err != nil {
		t.Fatalf("Chat #1 = %v", err)
	}
	if r1.Cached {
		t.Errorf("first call reported cached")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("provider calls after miss = %d, want 1", got)
	}

	// Second identical call: hit -> no dispatch.
	r2, err := e.Chat(ctx, req)
	if err != nil {
		t.Fatalf("Chat #2 = %v", err)
	}
	if !r2.Cached || r2.CacheLevel != cache.LevelL1 {
		t.Errorf("second call not served from L1: cached=%v level=%q", r2.Cached, r2.CacheLevel)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("provider calls after hit = %d, want 1 (no re-dispatch)", got)
	}
	// The cached response is identical to the fresh one.
	if r1.Response.Choices[0].Message.Content != r2.Response.Choices[0].Message.Content {
		t.Errorf("cached response differs from fresh response")
	}
}

func TestGateway_DifferentRequestsMiss(t *testing.T) {
	var calls int32
	e := newGateway(t, cache.DefaultConfig(), &calls)
	ctx := context.Background()

	_, _ = e.Chat(ctx, chatReq("first"))
	_, _ = e.Chat(ctx, chatReq("second"))
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("distinct requests dispatched %d times, want 2", got)
	}
}

func TestGateway_RouterErrorPropagates(t *testing.T) {
	var calls int32
	e := newGateway(t, cache.DefaultConfig(), &calls)

	// Request a model no provider supports -> routing returns no candidates.
	req := chatReq("hi")
	req.Model = "ghost-model"
	if _, err := e.Chat(context.Background(), req); err == nil {
		t.Fatalf("Chat(bad model) = nil error, want routing failure")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("provider dispatched despite routing failure")
	}
}

func TestGateway_CorruptCachedValueFallsThrough(t *testing.T) {
	var calls int32
	reg := provider.NewRegistry()
	_ = reg.Register(countingProvider("openai", &calls))
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, _ := routing.Build(pm, routing.DefaultConfig())

	l1 := cache.NewMemoryCache(cache.DefaultConfig().Memory)
	t.Cleanup(func() { _ = l1.Close() })
	cm := cache.NewManager([]cache.Cache{l1})
	e := gateway.New(router, cm, cache.DefaultConfig())

	req := chatReq("decode me")
	// Poison the cache under the exact key with a non-JSON value.
	key := cache.NewKeyGenerator().ChatKey("mock-chat", req)
	_ = cm.Set(context.Background(), key, []byte("not-json"), 0)

	res, err := e.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if res.Cached {
		t.Errorf("corrupt cached value should not be reported as a hit")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("provider should have been dispatched on decode failure")
	}
}

func TestGateway_Disabled(t *testing.T) {
	var calls int32
	cfg := cache.DefaultConfig()
	cfg.Enabled = false
	e := newGateway(t, cfg, &calls)
	ctx := context.Background()
	req := chatReq("hello")

	r1, _ := e.Chat(ctx, req)
	r2, _ := e.Chat(ctx, req)
	if r1.Cached || r2.Cached {
		t.Errorf("cache reported hits while disabled")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("disabled cache dispatched %d times, want 2", got)
	}
}
