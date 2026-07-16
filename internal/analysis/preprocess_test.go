package analysis

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func msg(role provider.Role, content string) provider.ChatMessage {
	return provider.ChatMessage{Role: role, Content: content}
}

func TestPreprocess_NormalizesWhitespace(t *testing.T) {
	p := NewPreprocessor()
	got := p.Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleUser, "hello   world\t\tfoo  \r\n\r\n\r\n\r\nbar   \n"),
	}})

	want := "hello world foo\n\nbar"
	if got.Text != want {
		t.Errorf("normalized text = %q, want %q", got.Text, want)
	}
}

func TestPreprocess_PreservesLeadingIndentation(t *testing.T) {
	p := NewPreprocessor()
	got := p.Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleUser, "def f():\n    return  1"),
	}})
	want := "def f():\n    return 1" // indent kept, internal double space collapsed
	if got.Text != want {
		t.Errorf("normalized = %q, want %q", got.Text, want)
	}
}

func TestPreprocess_CountsAndExtractsSystemPrompts(t *testing.T) {
	p := NewPreprocessor()
	got := p.Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleSystem, "You are helpful."),
		msg(provider.RoleUser, "Hi"),
		msg(provider.RoleAssistant, "Hello!"),
		msg(provider.RoleUser, "What is Go?"),
	}})

	if got.MessageCount != 4 {
		t.Errorf("message count = %d, want 4", got.MessageCount)
	}
	if got.UserTurns != 2 || got.AssistantTurns != 1 || got.SystemTurns != 1 {
		t.Errorf("turns user=%d assistant=%d system=%d, want 2/1/1", got.UserTurns, got.AssistantTurns, got.SystemTurns)
	}
	if len(got.SystemPrompts) != 1 || got.SystemPrompts[0] != "You are helpful." {
		t.Errorf("system prompts = %v", got.SystemPrompts)
	}
	if got.Prompt != "What is Go?" {
		t.Errorf("latest prompt = %q, want %q", got.Prompt, "What is Go?")
	}
}

func TestPreprocess_MaxBlankLinesOption(t *testing.T) {
	p := NewPreprocessor(WithMaxBlankLines(0))
	got := p.Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleUser, "a\n\n\nb"),
	}})
	if got.Text != "a\nb" {
		t.Errorf("with 0 blank lines = %q, want %q", got.Text, "a\nb")
	}
}
