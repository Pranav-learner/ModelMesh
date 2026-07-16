package analysis

import (
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestInstructionExtractor(t *testing.T) {
	cases := []struct {
		content string
		wantMin int
	}{
		{"What is Go?", 1}, // a single question counts as one instruction
		{"1. Write a function\n2. Add tests\n3. Document it", 3},
		{"- first\n- second\n- third", 3},
		{"Write a poem, then translate it, and finally summarize it.", 3},
	}
	for _, tc := range cases {
		f := extract(t, tc.content)
		if f.InstructionCount < tc.wantMin {
			t.Errorf("InstructionCount(%q) = %d, want >= %d", tc.content, f.InstructionCount, tc.wantMin)
		}
	}
}

func TestReasoningExtractor(t *testing.T) {
	reasoning := []string{
		"Explain step by step how quicksort works.",
		"Compare and contrast TCP and UDP.",
		"Prove that the square root of 2 is irrational.",
		"Analyze the trade-offs and justify your reasoning.",
	}
	for _, r := range reasoning {
		if extract(t, r).ReasoningIndicatorCount == 0 {
			t.Errorf("expected reasoning indicators for %q", r)
		}
	}
	if extract(t, "What time is it in Tokyo?").ReasoningIndicatorCount != 0 {
		t.Errorf("simple lookup should have no reasoning indicators")
	}
}

func TestEdgeCase_EmptyPrompt(t *testing.T) {
	f := extract(t, "")
	if f.InstructionCount != 0 || f.ReasoningIndicatorCount != 0 || f.HasCode {
		t.Errorf("empty prompt should produce zero features: %+v", f)
	}
}

func TestEdgeCase_WhitespaceOnly(t *testing.T) {
	f := extract(t, "   \n\n\t  ")
	if f.InstructionCount != 0 {
		t.Errorf("whitespace-only prompt should have 0 instructions, got %d", f.InstructionCount)
	}
}

func TestEdgeCase_VeryLargePrompt(t *testing.T) {
	// A large code+reasoning prompt must classify as Complex and not panic.
	big := "Refactor this and explain step by step why:\n```go\n" +
		strings.Repeat("func f() { x := 1 + 2; return x }\n", 300) + "```"
	e := New()
	res := e.Analyze(context.Background(), provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: big}}})
	if res.Classification.Complexity != ComplexityComplex {
		t.Errorf("large code+reasoning prompt = %s, want complex", res.Classification.Complexity)
	}
}
