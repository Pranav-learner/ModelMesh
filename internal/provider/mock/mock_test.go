package mock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func fixedClock() func() time.Time {
	ts := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return ts }
}

func TestMock_SatisfiesInterface(t *testing.T) {
	// Compile-time check is in mock.go; this is a runtime sanity check.
	var _ provider.LLMProvider = New()
}

func TestMock_ChatDeterministic(t *testing.T) {
	p := New(WithName("mock"), WithClock(fixedClock()))

	req := provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "be nice"},
			{Role: provider.RoleUser, Content: "ping"},
		},
	}

	r1, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	r2, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}

	if r1.Choices[0].Message.Content != r2.Choices[0].Message.Content {
		t.Errorf("mock Chat is not deterministic")
	}
	if r1.Provider != "mock" {
		t.Errorf("response Provider = %q, want mock", r1.Provider)
	}
	if r1.Choices[0].Message.Role != provider.RoleAssistant {
		t.Errorf("choice role = %q, want assistant", r1.Choices[0].Message.Role)
	}
	if r1.Usage.TotalTokens != r1.Usage.PromptTokens+r1.Usage.CompletionTokens {
		t.Errorf("usage totals inconsistent: %+v", r1.Usage)
	}
	if !r1.Created.Equal(r2.Created) {
		t.Errorf("clock not deterministic")
	}
}

func TestMock_ChatModelResolution(t *testing.T) {
	p := New()

	// Empty model resolves to the mock default.
	r, _ := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "x"}},
	})
	if r.Model != "mock-chat" {
		t.Errorf("Model = %q, want mock-chat", r.Model)
	}

	// Explicit model is echoed back.
	r, _ = p.Chat(context.Background(), provider.ChatRequest{
		Model:    "custom",
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "x"}},
	})
	if r.Model != "custom" {
		t.Errorf("Model = %q, want custom", r.Model)
	}
}

func TestMock_ChatValidationError(t *testing.T) {
	p := New()
	_, err := p.Chat(context.Background(), provider.ChatRequest{}) // no messages
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("Chat(invalid) = %v, want ErrInvalidRequest", err)
	}
}

func TestMock_Embeddings(t *testing.T) {
	p := New()
	req := provider.EmbeddingRequest{Input: []string{"hello", "world"}}

	r, err := p.Embeddings(context.Background(), req)
	if err != nil {
		t.Fatalf("Embeddings() = %v", err)
	}
	if len(r.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(r.Data))
	}
	for i, e := range r.Data {
		if e.Index != i {
			t.Errorf("Data[%d].Index = %d, want %d", i, e.Index, i)
		}
		if len(e.Vector) == 0 {
			t.Errorf("Data[%d].Vector is empty", i)
		}
	}
	// Determinism: same input yields the same vector.
	r2, _ := p.Embeddings(context.Background(), req)
	if r.Data[0].Vector[0] != r2.Data[0].Vector[0] {
		t.Errorf("embeddings not deterministic")
	}
}

func TestMock_Models(t *testing.T) {
	custom := provider.ModelInfo{ID: "only", Capabilities: []provider.Capability{provider.CapabilityChat}}
	p := New(WithModels(custom))

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() = %v", err)
	}
	if len(models) != 1 || models[0].ID != "only" {
		t.Fatalf("Models() = %+v, want [only]", models)
	}

	// Returned slice is a copy: mutation must not affect the provider.
	models[0].ID = "mutated"
	again, _ := p.Models(context.Background())
	if again[0].ID != "only" {
		t.Errorf("Models() returned a mutable reference to internal state")
	}
}

func TestMock_HealthCheck(t *testing.T) {
	p := New(
		WithHealth(provider.HealthStatus{State: provider.HealthStateDegraded, Detail: "warming up"}),
		WithClock(fixedClock()),
	)
	h, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck() = %v", err)
	}
	if h.State != provider.HealthStateDegraded {
		t.Errorf("State = %q, want degraded", h.State)
	}
	if h.CheckedAt.IsZero() {
		t.Errorf("CheckedAt not stamped")
	}
}

func TestMock_ErrorInjection(t *testing.T) {
	sentinel := provider.ErrProviderUnavailable
	p := New(WithName("faulty"), WithError(sentinel))

	_, chatErr := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "x"}},
	})
	if !errors.Is(chatErr, sentinel) {
		t.Errorf("Chat error = %v, want wrapped ErrProviderUnavailable", chatErr)
	}

	_, embErr := p.Embeddings(context.Background(), provider.EmbeddingRequest{Input: []string{"x"}})
	if !errors.Is(embErr, sentinel) {
		t.Errorf("Embeddings error = %v, want wrapped sentinel", embErr)
	}

	var pe *provider.ProviderError
	if !errors.As(chatErr, &pe) || pe.Provider != "faulty" {
		t.Errorf("error not wrapped as ProviderError with provider name")
	}
}

func TestMock_ContextCancellation(t *testing.T) {
	p := New(WithLatency(time.Hour)) // would block if latency were awaited

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, err := p.Chat(ctx, provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "x"}},
	})
	if !errors.Is(err, provider.ErrTimeout) {
		t.Fatalf("cancelled Chat = %v, want ErrTimeout", err)
	}
}

func TestMock_CustomChatFunc(t *testing.T) {
	want := provider.ChatResponse{Provider: "mock", Model: "scripted"}
	p := New(WithChatFunc(func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return want, nil
	}))

	got, err := p.Chat(context.Background(), provider.ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if got.Model != "scripted" {
		t.Errorf("custom chat func not used: %+v", got)
	}
}
