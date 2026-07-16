package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/provider/adapters/anthropic"
	"github.com/symbiotes/modelmesh/internal/retry"
)

const messageBody = `{
  "id": "msg_1",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-5",
  "content": [{"type": "text", "text": "hi there"}],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": 10, "output_tokens": 5, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
}`

const anthModelsBody = `{"data":[{"id":"claude-sonnet-4-5","type":"model","display_name":"Claude","created_at":"2025-01-01T00:00:00Z"}],"has_more":false}`

func anthErrBody(msg string) string {
	return `{"type":"error","error":{"type":"authentication_error","message":"` + msg + `"}}`
}

func newProvider(t *testing.T, handler http.HandlerFunc) *anthropic.Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return anthropic.New(anthropic.Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Timeout: 2 * time.Second,
		Retry:   retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
	})
}

func mux(messages, models func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "messages"):
			messages(w, r)
		case strings.Contains(r.URL.Path, "models"):
			models(w, r)
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestProvider_Name(t *testing.T) {
	if anthropic.New(anthropic.Config{}).Name() != "anthropic" {
		t.Errorf("default name wrong")
	}
}

func TestProvider_Chat(t *testing.T) {
	var gotBody map[string]any
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			writeJSON(w, http.StatusOK, messageBody)
		}, nil,
	))

	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "be terse"},
			{Role: provider.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}

	if resp.Provider != "anthropic" || resp.Model != "claude-sonnet-4-5" || resp.ID != "msg_1" {
		t.Errorf("response header wrong: %+v", resp)
	}
	if resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// System prompt is lifted to a top-level field, not a message.
	if _, hasSystem := gotBody["system"]; !hasSystem {
		t.Errorf("system prompt not sent as top-level field: %v", gotBody)
	}
	if msgs, ok := gotBody["messages"].([]any); !ok || len(msgs) != 1 {
		t.Errorf("messages should exclude system turn: %v", gotBody["messages"])
	}
}

func TestProvider_EmbeddingsUnsupported(t *testing.T) {
	p := anthropic.New(anthropic.Config{APIKey: "k"})

	_, err := p.Embeddings(context.Background(), provider.EmbeddingRequest{Input: []string{"x"}})
	if !errors.Is(err, provider.ErrNotImplemented) {
		t.Fatalf("Embeddings() = %v, want ErrNotImplemented", err)
	}
	var pe *provider.ProviderError
	if !errors.As(err, &pe) || pe.Provider != "anthropic" {
		t.Errorf("error not wrapped as ProviderError: %v", err)
	}
}

func TestProvider_Models_NoNetwork(t *testing.T) {
	p := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Models() made a network call")
		http.Error(w, "no", 500)
	})
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() = %v", err)
	}
	if len(models) == 0 {
		t.Errorf("empty catalog")
	}
	for _, m := range models {
		if m.Supports(provider.CapabilityEmbeddings) {
			t.Errorf("anthropic must not advertise embeddings: %s", m.ID)
		}
	}
}

func TestProvider_HealthCheck_Healthy(t *testing.T) {
	p := newProvider(t, mux(nil,
		func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, anthModelsBody) },
	))
	h, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck() = %v", err)
	}
	if h.State != provider.HealthStateHealthy || h.Provider != "anthropic" {
		t.Errorf("health = %+v", h)
	}
}

func TestProvider_HealthCheck_Unhealthy(t *testing.T) {
	p := newProvider(t, mux(nil,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusInternalServerError, anthErrBody("boom"))
		},
	))
	h, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck() error = %v, want nil with unhealthy status", err)
	}
	if h.State != provider.HealthStateUnhealthy || h.Detail == "" {
		t.Errorf("health = %+v", h)
	}
}

func TestProvider_ErrorTranslation(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, provider.ErrAuthenticationFailed},
		{http.StatusNotFound, provider.ErrModelNotFound},
		{http.StatusTooManyRequests, provider.ErrRateLimited},
		{http.StatusInternalServerError, provider.ErrProviderUnavailable},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, c.status, anthErrBody("x"))
			}))
			defer srv.Close()
			p := anthropic.New(anthropic.Config{APIKey: "k", BaseURL: srv.URL, Retry: retry.Policy{MaxRetries: 0}})

			_, err := p.Chat(context.Background(), provider.ChatRequest{
				Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
			})
			if !errors.Is(err, c.want) {
				t.Errorf("status %d -> %v, want %v", c.status, err, c.want)
			}
		})
	}
}

func TestProvider_RetriesTransientThenSucceeds(t *testing.T) {
	var calls int32
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&calls, 1) < 3 {
				writeJSON(w, http.StatusServiceUnavailable, anthErrBody("later"))
				return
			}
			writeJSON(w, 200, messageBody)
		}, nil,
	))

	_, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() = %v, want success after retries", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestProvider_ContextCancelled(t *testing.T) {
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			writeJSON(w, 200, messageBody)
		}, nil,
	))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := p.Chat(ctx, provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, provider.ErrTimeout) {
		t.Fatalf("Chat(cancelled) = %v, want ErrTimeout", err)
	}
}
