package shadow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/mock"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// controllable is a mock provider whose Chat can block, fail, or panic.
func controllable(name string, gate <-chan struct{}, fail error, doPanic bool) *mock.Provider {
	return mock.New(
		mock.WithName(name),
		mock.WithModels(provider.ModelInfo{ID: "m", Capabilities: []provider.Capability{provider.CapabilityChat}}),
		mock.WithChatFunc(func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
			if gate != nil {
				<-gate
			}
			if doPanic {
				panic("boom")
			}
			if fail != nil {
				return provider.ChatResponse{}, fail
			}
			return provider.ChatResponse{ID: "r", Provider: name, Model: req.Model,
				Choices: []provider.Choice{{Message: provider.ChatMessage{Role: provider.RoleAssistant, Content: "shadow ok"}}}}, nil
		}),
	)
}

func sourceOf(t *testing.T, providers ...*mock.Provider) *provider.Manager {
	t.Helper()
	reg := provider.NewRegistry()
	for _, p := range providers {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return provider.NewManager(reg, provider.WithDefaultProvider(providers[0].Name()))
}

// alwaysSampler forces sampling; neverSampler suppresses it (for 50% policies).
func alwaysSampler() float64 { return 0 }
func neverSampler() float64  { return 0.999 }

func chatReq() provider.ChatRequest {
	return provider.ChatRequest{Model: "m", Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}}
}

func TestManager_DisabledNeverShadows(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, false))
	m, err := shadow.New(shadow.DefaultConfig(), pm)
	if err != nil {
		t.Fatal(err)
	}
	exec, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"})
	if ok || exec != nil {
		t.Errorf("disabled manager should not shadow")
	}
	if s := m.Stats(); s.Evaluated != 1 || s.Sampled != 0 {
		t.Errorf("stats = %+v, want evaluated 1 / sampled 0", s)
	}
}

func TestManager_FixedPercentageDispatchesToSecondary(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, false))
	m, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 50},
		pm, shadow.WithSampler(alwaysSampler),
	)
	if err != nil {
		t.Fatal(err)
	}

	exec, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"})
	if !ok || exec == nil {
		t.Fatal("expected a shadow to be dispatched")
	}
	// Secondary is selected independently and excludes the primary.
	if exec.Request.Target.Provider != "anthropic" {
		t.Errorf("shadow target = %q, want anthropic (independent of primary)", exec.Request.Target.Provider)
	}
	if exec.Metadata.Primary.Provider != "openai" || exec.Metadata.Policy != shadow.PolicyFixedPercentage {
		t.Errorf("metadata wrong: %+v", exec.Metadata)
	}

	m.Wait()
	res := exec.Wait()
	if !res.Success || res.Response.Provider != "anthropic" {
		t.Errorf("shadow result = %+v, want success from anthropic", res)
	}
	if s := m.Stats(); s.Dispatched != 1 || s.Succeeded != 1 {
		t.Errorf("stats = %+v, want dispatched 1 / succeeded 1", s)
	}
}

func TestManager_PercentageSuppressed(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, false))
	m, _ := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 50},
		pm, shadow.WithSampler(neverSampler),
	)
	if _, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"}); ok {
		t.Errorf("neverSampler should suppress a 50%% policy")
	}
}

func TestManager_AsyncExecution(t *testing.T) {
	gate := make(chan struct{})
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", gate, nil, false))
	m, _ := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(alwaysSampler),
	)

	exec, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"})
	if !ok {
		t.Fatal("expected dispatch")
	}
	// Shadow() returned without waiting for the (blocked) shadow request.
	if _, done := exec.Result(); done {
		t.Errorf("shadow should still be running while the provider is gated")
	}
	close(gate) // release the shadow provider
	m.Wait()
	if _, done := exec.Result(); !done {
		t.Errorf("shadow should have completed after release")
	}
}

func TestManager_FailureIsolation_Error(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, errors.New("provider exploded"), false))
	m, _ := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(alwaysSampler),
	)

	// Shadow() never returns an error even though the shadow provider fails.
	exec, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"})
	if !ok {
		t.Fatal("expected dispatch")
	}
	m.Wait()
	res := exec.Wait()
	if res.Success || res.Err == "" {
		t.Errorf("failed shadow should record failure: %+v", res)
	}
	if s := m.Stats(); s.Failed != 1 {
		t.Errorf("stats = %+v, want failed 1", s)
	}
}

func TestManager_FailureIsolation_Panic(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, true))
	m, _ := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(alwaysSampler),
	)

	exec, _ := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"})
	m.Wait() // must not crash the test process
	res := exec.Wait()
	if res.Success {
		t.Errorf("panicking shadow should be recorded as failure")
	}
	if res.Err == "" {
		t.Errorf("panic should be captured in the result error")
	}
}

func TestManager_NoSecondaryAvailable(t *testing.T) {
	// Only the primary provider is registered → nothing to shadow to.
	pm := sourceOf(t, controllable("openai", nil, nil, false))
	m, _ := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm, shadow.WithSampler(alwaysSampler),
	)
	if _, ok := m.Shadow(context.Background(), chatReq(), shadow.Target{Provider: "openai", Model: "m"}); ok {
		t.Errorf("no secondary should yield no shadow")
	}
	if s := m.Stats(); s.Sampled != 1 || s.Skipped != 1 || s.Dispatched != 0 {
		t.Errorf("stats = %+v, want sampled 1 / skipped 1 / dispatched 0", s)
	}
}

func TestManager_NilProviderSource(t *testing.T) {
	if _, err := shadow.New(shadow.DefaultConfig(), nil); !errors.Is(err, shadow.ErrInvalidConfig) {
		t.Errorf("nil provider source = %v, want ErrInvalidConfig", err)
	}
}
