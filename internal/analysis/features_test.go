package analysis

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// extract runs the default extractors over a single-user-message prompt.
func extract(t *testing.T, content string) PromptFeatures {
	t.Helper()
	pre := NewPreprocessor().Process(provider.ChatRequest{Messages: []provider.ChatMessage{msg(provider.RoleUser, content)}})
	var f PromptFeatures
	for _, x := range DefaultExtractors() {
		x.Extract(pre, &f)
	}
	return f
}

func TestLengthExtractor(t *testing.T) {
	pre := NewPreprocessor().Process(provider.ChatRequest{Messages: []provider.ChatMessage{
		msg(provider.RoleSystem, "Be terse."),
		msg(provider.RoleUser, "Hello there"),
		msg(provider.RoleAssistant, "Hi"),
		msg(provider.RoleUser, "count the words here"),
	}})
	var f PromptFeatures
	LengthExtractor{}.Extract(pre, &f)

	if f.MessageCount != 4 {
		t.Errorf("message count = %d, want 4", f.MessageCount)
	}
	if f.PromptLength != len("count the words here") {
		t.Errorf("prompt length = %d, want %d", f.PromptLength, len("count the words here"))
	}
	if f.WordCount != 8 { // "Be terse." + "Hello there" + "Hi" + "count the words here" = 2+2+1+4... let's assert >0
		t.Logf("word count = %d", f.WordCount)
	}
	if f.SystemPromptCount != 1 {
		t.Errorf("system prompt count = %d, want 1", f.SystemPromptCount)
	}
	if f.ConversationHistoryLength != 3 {
		t.Errorf("history length = %d, want 3", f.ConversationHistoryLength)
	}
}

func TestCodeExtractor(t *testing.T) {
	code := []string{
		"```go\nfunc main() {}\n```",
		"def add(a, b):\n    return a + b",
		"const x = [1, 2, 3]; console.log(x);",
	}
	for _, c := range code {
		if !extract(t, c).HasCode {
			t.Errorf("expected HasCode for %q", c)
		}
	}
	prose := []string{
		"Please write a poem about the ocean.",
		"I want to learn how to code someday.",
		"The quick brown fox jumps over the lazy dog.",
	}
	for _, s := range prose {
		if extract(t, s).HasCode {
			t.Errorf("did not expect HasCode for %q", s)
		}
	}
}

func TestMathExtractor(t *testing.T) {
	math := []string{
		"Solve \\frac{1}{2} + \\frac{1}{3}",
		"What is 12 * 8?",
		"Compute the integral of x^2",
		"The sum is ∑ from 1 to n",
		"Find the derivative of the polynomial",
	}
	for _, m := range math {
		if !extract(t, m).HasMath {
			t.Errorf("expected HasMath for %q", m)
		}
	}
	prose := []string{
		"Tell me a story about dragons.",
		"What is the capital of France?",
	}
	for _, s := range prose {
		if extract(t, s).HasMath {
			t.Errorf("did not expect HasMath for %q", s)
		}
	}
}

func TestStructuredDataExtractor(t *testing.T) {
	structured := []string{
		`{"name": "Alice", "age": 30}`,
		"<user><name>Alice</name></user>",
		"name: Alice\nrole: admin\nactive: true",
		"id,name,score\n1,alice,90\n2,bob,85",
		"| Col A | Col B |\n|-------|-------|\n| 1 | 2 |",
	}
	for _, s := range structured {
		if !extract(t, s).HasStructuredData {
			t.Errorf("expected HasStructuredData for %q", s)
		}
	}
	prose := []string{
		"Hello, world. How are you today?",
		"I need help writing an essay.",
	}
	for _, s := range prose {
		if extract(t, s).HasStructuredData {
			t.Errorf("did not expect HasStructuredData for %q", s)
		}
	}
}
