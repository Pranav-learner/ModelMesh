package analysis

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestHeuristicEstimator(t *testing.T) {
	est := NewHeuristicEstimator()
	pre := NewPreprocessor().Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleUser, "abcdefgh"), // 8 chars → 8/4 = 2 tokens
	}})

	got := est.Estimate(pre, provider.ChatRequest{})
	if got.InputTokens != 2 {
		t.Errorf("input tokens = %d, want 2", got.InputTokens)
	}
	if got.ExpectedOutputTokens != DefaultExpectedOutputTokens {
		t.Errorf("expected output = %d, want default %d", got.ExpectedOutputTokens, DefaultExpectedOutputTokens)
	}
	if got.EstimatedTotalTokens != got.InputTokens+got.ExpectedOutputTokens {
		t.Errorf("total = %d, want input+output", got.EstimatedTotalTokens)
	}
}

func TestHeuristicEstimator_MaxTokensOverridesOutput(t *testing.T) {
	est := NewHeuristicEstimator()
	pre := NewPreprocessor().Process(provider.ChatRequest{Messages: []provider.ChatMessage{msg(provider.RoleUser, "hello world")}})
	got := est.Estimate(pre, provider.ChatRequest{MaxTokens: 1500})
	if got.ExpectedOutputTokens != 1500 {
		t.Errorf("expected output = %d, want 1500 (from MaxTokens)", got.ExpectedOutputTokens)
	}
}

func TestHeuristicEstimator_EmptyAndOptions(t *testing.T) {
	est := NewHeuristicEstimator(WithCharsPerToken(2), WithDefaultOutputTokens(50))
	empty := est.Estimate(Preprocessed{}, provider.ChatRequest{})
	if empty.InputTokens != 0 || empty.EstimatedTotalTokens != 50 {
		t.Errorf("empty estimate = %+v, want 0 input / 50 total", empty)
	}
	pre := NewPreprocessor().Process(provider.ChatRequest{Messages: []provider.ChatMessage{msg(provider.RoleUser, "abcd")}})
	got := est.Estimate(pre, provider.ChatRequest{}) // 4 chars / 2 = 2 tokens
	if got.InputTokens != 2 {
		t.Errorf("input tokens = %d, want 2 (2 chars/token)", got.InputTokens)
	}
}
