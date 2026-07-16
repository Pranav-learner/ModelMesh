package gateway_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/routing"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// shadowProvider records how many times it was called (as the secondary) and can
// be made to fail, to prove shadow failures never reach the application.
func shadowProvider(name string, calls *int32, fail error) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithModels(provider.ModelInfo{ID: "claude-sonnet", Capabilities: []provider.Capability{provider.CapabilityChat}}),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			atomic.AddInt32(calls, 1)
			if fail != nil {
				return provider.ChatResponse{}, fail
			}
			return provider.ChatResponse{ID: "s", Provider: name, Model: req.Model,
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "shadow"}}}}, nil
		}),
	)
}

func newShadowGateway(t *testing.T, shadowFail error, secondaryCalls *int32) (*gateway.Engine, *shadow.Manager) {
	t.Helper()
	primary := optProvider("openai", "gpt-4") // the primary, returns "openai"
	secondary := shadowProvider("anthropic", secondaryCalls, shadowFail)

	reg := provider.NewRegistry()
	if err := reg.Register(primary); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(secondary); err != nil {
		t.Fatal(err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	router, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	sm, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(func() float64 { return 0 }), // always sample
	)
	if err != nil {
		t.Fatal(err)
	}

	gw := gateway.New(router, cache.NewManager(nil), cache.Config{Enabled: false},
		gateway.WithProviderResolver(pm),
		gateway.WithShadow(sm),
	)
	t.Cleanup(sm.Wait)
	return gw, sm
}

func TestGatewayShadow_AppReceivesOnlyPrimary(t *testing.T) {
	var secondaryCalls int32
	gw, _ := newShadowGateway(t, nil, &secondaryCalls)

	res, err := gw.Chat(context.Background(), provider.ChatRequest{
		Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	// The application sees the PRIMARY provider only.
	if res.Response.Provider != "openai" {
		t.Errorf("application received %q, want the primary openai", res.Response.Provider)
	}
	// No shadow data leaks onto the result.
	if res.Response.Choices[0].Message.Content == "shadow" {
		t.Errorf("shadow response leaked into the primary result")
	}
}

func TestGatewayShadow_FailingShadowDoesNotAffectPrimary(t *testing.T) {
	var secondaryCalls int32
	gw, _ := newShadowGateway(t, errors.New("shadow provider down"), &secondaryCalls)

	// Even though the shadow provider always fails, the primary succeeds.
	res, err := gw.Chat(context.Background(), provider.ChatRequest{
		Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("primary must succeed despite shadow failure, got: %v", err)
	}
	if res.Response.Provider != "openai" {
		t.Errorf("primary provider = %q, want openai", res.Response.Provider)
	}
}

func TestGatewayShadow_DispatchesToSecondary(t *testing.T) {
	var secondaryCalls int32
	gw, sm := newShadowGateway(t, nil, &secondaryCalls)

	const n = 5
	for i := 0; i < n; i++ {
		if _, err := gw.Chat(context.Background(), provider.ChatRequest{
			Model: "gpt-4", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	}
	sm.Wait() // drain in-flight shadows

	if got := atomic.LoadInt32(&secondaryCalls); got != n {
		t.Errorf("secondary provider called %d times, want %d (100%% sampling)", got, n)
	}
	if s := sm.Stats(); s.Dispatched != n || s.Succeeded != n {
		t.Errorf("shadow stats = %+v, want dispatched/succeeded %d", s, n)
	}
}
