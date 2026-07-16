package anthropic

import (
	"testing"
	"time"

	ant "github.com/anthropics/anthropic-sdk-go"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func f64(v float64) *float64 { return &v }

func TestSplitSystem(t *testing.T) {
	msgs := []provider.ChatMessage{
		{Role: provider.RoleSystem, Content: "be terse"},
		{Role: provider.RoleUser, Content: "hi"},
		{Role: provider.RoleAssistant, Content: "hello"},
		{Role: provider.RoleUser, Content: "again"},
	}
	system, turns := splitSystem(msgs)

	if len(system) != 1 || system[0].Text != "be terse" {
		t.Errorf("system = %+v, want single 'be terse'", system)
	}
	if len(turns) != 3 {
		t.Errorf("turns = %d, want 3 (system excluded)", len(turns))
	}
}

func TestToMessageParams_DefaultsMaxTokens(t *testing.T) {
	// Anthropic requires max_tokens; unset must become the default.
	params := toMessageParams(provider.ChatRequest{
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	}, "claude-sonnet-4-5")

	if params.MaxTokens != defaultMaxTokens {
		t.Errorf("MaxTokens = %d, want default %d", params.MaxTokens, defaultMaxTokens)
	}
	if params.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q", params.Model)
	}
}

func TestToMessageParams_HonorsFields(t *testing.T) {
	params := toMessageParams(provider.ChatRequest{
		Messages:    []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
		MaxTokens:   256,
		Temperature: f64(0.4),
		TopP:        f64(0.8),
		Stop:        []string{"STOP"},
	}, "claude-opus-4-1")

	if params.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", params.MaxTokens)
	}
	if params.Temperature.Or(-1) != 0.4 {
		t.Errorf("Temperature = %v, want 0.4", params.Temperature.Or(-1))
	}
	if len(params.StopSequences) != 1 || params.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %+v", params.StopSequences)
	}
}

func TestFromMessage(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	msg := &ant.Message{
		ID:         "msg_1",
		Model:      ant.Model("claude-sonnet-4-5"),
		StopReason: ant.StopReason("end_turn"),
		Content: []ant.ContentBlockUnion{
			{Type: "text", Text: "hello "},
			{Type: "text", Text: "world"},
		},
		Usage: ant.Usage{InputTokens: 10, OutputTokens: 5},
	}

	out := fromMessage("anthropic", msg, now)

	if out.ID != "msg_1" || out.Provider != "anthropic" || out.Model != "claude-sonnet-4-5" {
		t.Errorf("header wrong: %+v", out)
	}
	if out.Choices[0].Message.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", out.Choices[0].Message.Content)
	}
	if out.Choices[0].FinishReason != provider.FinishReasonStop {
		t.Errorf("finish = %q, want stop", out.Choices[0].FinishReason)
	}
	if out.Usage.PromptTokens != 10 || out.Usage.CompletionTokens != 5 || out.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", out.Usage)
	}
	if !out.Created.Equal(now) {
		t.Errorf("created = %v, want %v", out.Created, now)
	}
}

func TestMapStopReason(t *testing.T) {
	cases := map[string]provider.FinishReason{
		"end_turn":      provider.FinishReasonStop,
		"stop_sequence": provider.FinishReasonStop,
		"max_tokens":    provider.FinishReasonLength,
		"tool_use":      provider.FinishReasonStop,
		"":              provider.FinishReasonStop,
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("mapStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelsFromIDs(t *testing.T) {
	models := ModelsFromIDs([]string{"claude-sonnet-4-5", "claude-haiku-4-5"})
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if !models[0].Supports(provider.CapabilityChat) {
		t.Errorf("claude model should be chat-capable")
	}
}
