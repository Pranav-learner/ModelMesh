package openai_test

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
	"github.com/symbiotes/modelmesh/internal/provider/adapters/openai"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// canned response bodies -----------------------------------------------------

const chatBody = `{
  "id": "chatcmpl-1",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "gpt-4o",
  "choices": [
    {"index": 0, "message": {"role": "assistant", "content": "hi there"}, "finish_reason": "stop"}
  ],
  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
}`

const embedBody = `{
  "object": "list",
  "model": "text-embedding-3-small",
  "data": [{"object": "embedding", "index": 0, "embedding": [0.1, 0.2, 0.3]}],
  "usage": {"prompt_tokens": 3, "total_tokens": 3}
}`

const modelsBody = `{"object":"list","data":[{"id":"gpt-4o","object":"model","created":1,"owned_by":"openai"}]}`

func errBody(msg string) string {
	return `{"error":{"message":"` + msg + `","type":"invalid_request_error","code":"x"}}`
}

// newProvider spins an httptest server with the given handler and returns an
// OpenAI provider pointed at it, with a fast retry policy for tests.
func newProvider(t *testing.T, handler http.HandlerFunc) *openai.Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return openai.New(openai.Config{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Timeout: 2 * time.Second,
		Retry:   retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
	})
}

// mux routes by path suffix so it is robust to base-URL joining.
func mux(chat, embed, models func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "chat/completions"):
			chat(w, r)
		case strings.Contains(r.URL.Path, "embeddings"):
			embed(w, r)
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

// tests ----------------------------------------------------------------------

func TestProvider_Name(t *testing.T) {
	p := openai.New(openai.Config{})
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", p.Name())
	}
	custom := openai.New(openai.Config{Name: "azure-openai"})
	if custom.Name() != "azure-openai" {
		t.Errorf("Name() = %q, want azure-openai", custom.Name())
	}
}

func TestProvider_Chat(t *testing.T) {
	var gotBody map[string]any
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &gotBody)
			writeJSON(w, http.StatusOK, chatBody)
		},
		nil, nil,
	))

	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}

	// Response normalization.
	if resp.Provider != "openai" || resp.Model != "gpt-4o" || resp.ID != "chatcmpl-1" {
		t.Errorf("response header wrong: %+v", resp)
	}
	if resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// Request translation: the outgoing body carried our model + message.
	if gotBody["model"] != "gpt-4o" {
		t.Errorf("outgoing model = %v, want gpt-4o", gotBody["model"])
	}
	if msgs, ok := gotBody["messages"].([]any); !ok || len(msgs) != 1 {
		t.Errorf("outgoing messages = %v", gotBody["messages"])
	}
}

func TestProvider_ChatValidationFailsFast(t *testing.T) {
	called := int32(0)
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&called, 1); writeJSON(w, 200, chatBody) },
		nil, nil,
	))

	_, err := p.Chat(context.Background(), provider.ChatRequest{}) // no messages
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("Chat(invalid) = %v, want ErrInvalidRequest", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Errorf("provider was called despite invalid request")
	}
}

func TestProvider_DefaultModelResolution(t *testing.T) {
	var gotModel string
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			var b map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &b)
			gotModel, _ = b["model"].(string)
			writeJSON(w, 200, chatBody)
		}, nil, nil,
	))

	_, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() = %v", err)
	}
	if gotModel != "gpt-4.1" { // first chat-capable default model
		t.Errorf("default model = %q, want gpt-4.1", gotModel)
	}
}

func TestProvider_Embeddings(t *testing.T) {
	p := newProvider(t, mux(
		nil,
		func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, embedBody) },
		nil,
	))

	resp, err := p.Embeddings(context.Background(), provider.EmbeddingRequest{
		Model: "text-embedding-3-small",
		Input: []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Embeddings() = %v", err)
	}
	if resp.Provider != "openai" || len(resp.Data) != 1 || len(resp.Data[0].Vector) != 3 {
		t.Errorf("embeddings response wrong: %+v", resp)
	}
	if resp.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestProvider_Models_NoNetwork(t *testing.T) {
	// Models() must not hit the server; use a handler that fails the test if called.
	p := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Models() made a network call to %s", r.URL.Path)
		http.Error(w, "no", 500)
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() = %v", err)
	}
	if len(models) == 0 {
		t.Errorf("Models() returned empty catalog")
	}
}

func TestProvider_HealthCheck_Healthy(t *testing.T) {
	p := newProvider(t, mux(nil, nil,
		func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, modelsBody) },
	))

	h, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck() = %v", err)
	}
	if h.State != provider.HealthStateHealthy {
		t.Errorf("state = %q, want healthy", h.State)
	}
	if h.Provider != "openai" {
		t.Errorf("provider = %q, want openai", h.Provider)
	}
}

func TestProvider_HealthCheck_Unhealthy(t *testing.T) {
	p := newProvider(t, mux(nil, nil,
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusUnauthorized, errBody("bad key"))
		},
	))

	h, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck() returned error, want nil with unhealthy status: %v", err)
	}
	if h.State != provider.HealthStateUnhealthy {
		t.Errorf("state = %q, want unhealthy", h.State)
	}
	if h.Detail == "" {
		t.Errorf("unhealthy status should carry a detail message")
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
			p := openai.New(openai.Config{
				APIKey:  "k",
				BaseURL: serverReturning(t, c.status, errBody("x")),
				Retry:   retry.Policy{MaxRetries: 0},
			})
			_, err := p.Chat(context.Background(), provider.ChatRequest{
				Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
			})
			if !errors.Is(err, c.want) {
				t.Errorf("status %d -> %v, want %v", c.status, err, c.want)
			}
			var pe *provider.ProviderError
			if !errors.As(err, &pe) || pe.Provider != "openai" {
				t.Errorf("error not wrapped as ProviderError: %v", err)
			}
		})
	}
}

func TestProvider_RetriesTransientThenSucceeds(t *testing.T) {
	var calls int32
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&calls, 1) < 3 {
				writeJSON(w, http.StatusServiceUnavailable, errBody("try later"))
				return
			}
			writeJSON(w, 200, chatBody)
		}, nil, nil,
	))

	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() = %v, want success after retries", err)
	}
	if resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("unexpected content: %q", resp.Choices[0].Message.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3 (2 retries)", got)
	}
}

func TestProvider_DoesNotRetryNonTransient(t *testing.T) {
	var calls int32
	p := newProvider(t, mux(
		func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			writeJSON(w, http.StatusUnauthorized, errBody("bad key"))
		}, nil, nil,
	))

	_, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, provider.ErrAuthenticationFailed) {
		t.Fatalf("err = %v, want ErrAuthenticationFailed", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (auth error must not retry)", got)
	}
}

// serverReturning starts a server that always responds with the given status and
// body, and returns its URL. It is cleaned up when the test ends.
func serverReturning(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, status, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
