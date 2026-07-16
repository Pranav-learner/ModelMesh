package openai

import (
	"testing"

	oai "github.com/openai/openai-go"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func f64(v float64) *float64 { return &v }

func TestToChatParams(t *testing.T) {
	req := provider.ChatRequest{
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "hi"},
			{Role: provider.RoleAssistant, Content: "prev"},
		},
		MaxTokens:   64,
		Temperature: f64(0.5),
		TopP:        f64(0.9),
		Stop:        []string{"END"},
	}

	params := toChatParams(req, "gpt-4o")

	if params.Model != oai.ChatModel("gpt-4o") {
		t.Errorf("Model = %q, want gpt-4o", params.Model)
	}
	if len(params.Messages) != 3 {
		t.Errorf("len(Messages) = %d, want 3", len(params.Messages))
	}
	if params.MaxTokens.Or(0) != 64 {
		t.Errorf("MaxTokens = %d, want 64", params.MaxTokens.Or(0))
	}
	if params.Temperature.Or(-1) != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", params.Temperature.Or(-1))
	}
	if len(params.Stop.OfStringArray) != 1 || params.Stop.OfStringArray[0] != "END" {
		t.Errorf("Stop = %+v, want [END]", params.Stop.OfStringArray)
	}
}

func TestToChatParams_OmitsUnsetOptionals(t *testing.T) {
	req := provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}}}
	params := toChatParams(req, "gpt-4o")

	if params.MaxTokens.Valid() {
		t.Errorf("MaxTokens should be omitted when unset")
	}
	if params.Temperature.Valid() {
		t.Errorf("Temperature should be omitted when unset")
	}
}

func TestFromChatCompletion(t *testing.T) {
	resp := &oai.ChatCompletion{
		ID:      "chatcmpl-1",
		Model:   "gpt-4o",
		Created: 1_700_000_000,
		Choices: []oai.ChatCompletionChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message:      oai.ChatCompletionMessage{Content: "hello"},
			},
		},
		Usage: oai.CompletionUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	out := fromChatCompletion("openai", resp)

	if out.ID != "chatcmpl-1" || out.Model != "gpt-4o" || out.Provider != "openai" {
		t.Errorf("header fields wrong: %+v", out)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "hello" {
		t.Errorf("choices wrong: %+v", out.Choices)
	}
	if out.Choices[0].Message.Role != provider.RoleAssistant {
		t.Errorf("role = %q, want assistant", out.Choices[0].Message.Role)
	}
	if out.Choices[0].FinishReason != provider.FinishReasonStop {
		t.Errorf("finish reason = %q, want stop", out.Choices[0].FinishReason)
	}
	if out.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", out.Usage)
	}
	if out.Created.Unix() != 1_700_000_000 {
		t.Errorf("created = %v", out.Created)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]provider.FinishReason{
		"stop":           provider.FinishReasonStop,
		"length":         provider.FinishReasonLength,
		"content_filter": provider.FinishReasonContentFilter,
		"tool_calls":     provider.FinishReasonStop,
		"function_call":  provider.FinishReasonStop,
		"weird":          provider.FinishReasonStop,
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromEmbeddingResponse(t *testing.T) {
	resp := &oai.CreateEmbeddingResponse{
		Model: "text-embedding-3-small",
		Data: []oai.Embedding{
			{Index: 0, Embedding: []float64{0.1, 0.2}},
			{Index: 1, Embedding: []float64{0.3}},
		},
		Usage: oai.CreateEmbeddingResponseUsage{PromptTokens: 3, TotalTokens: 3},
	}

	out := fromEmbeddingResponse("openai", resp)

	if out.Provider != "openai" || out.Model != "text-embedding-3-small" {
		t.Errorf("header fields wrong: %+v", out)
	}
	if len(out.Data) != 2 || out.Data[1].Index != 1 || len(out.Data[0].Vector) != 2 {
		t.Errorf("data wrong: %+v", out.Data)
	}
	if out.Usage.TotalTokens != 3 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestModelsFromIDs(t *testing.T) {
	models := ModelsFromIDs([]string{"gpt-4o", "text-embedding-3-small"})
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if !models[0].Supports(provider.CapabilityChat) {
		t.Errorf("gpt-4o should be chat-capable")
	}
	if !models[1].Supports(provider.CapabilityEmbeddings) {
		t.Errorf("embedding model should be embeddings-capable")
	}
}
