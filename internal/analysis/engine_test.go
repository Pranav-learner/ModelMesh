package analysis_test

import (
	"context"
	"testing"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func codeRequest() provider.ChatRequest {
	return provider.ChatRequest{
		MaxTokens: 800,
		Messages: []provider.ChatMessage{
			{Role: provider.RoleSystem, Content: "You are a senior engineer."},
			{Role: provider.RoleUser, Content: "Earlier we discussed sorting."},
			{Role: provider.RoleAssistant, Content: "Yes, quicksort."},
			{Role: provider.RoleUser, Content: "```go\nfunc sum(a, b int) int { return a + b }\n```\nExplain this."},
		},
	}
}

func TestEngine_AnalyzePipeline(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), codeRequest())

	// Preprocessing.
	if res.Preprocessed.MessageCount != 4 || res.Preprocessed.SystemTurns != 1 {
		t.Errorf("preprocess counts wrong: %+v", res.Preprocessed)
	}

	// Feature extraction.
	if !res.Features.HasCode {
		t.Errorf("expected HasCode")
	}
	if res.Features.MessageCount != 4 {
		t.Errorf("feature message count = %d, want 4", res.Features.MessageCount)
	}
	if res.Features.ConversationHistoryLength != 3 {
		t.Errorf("history length = %d, want 3", res.Features.ConversationHistoryLength)
	}
	if res.Features.WordCount == 0 || res.Features.CharCount == 0 {
		t.Errorf("length features not populated: %+v", res.Features)
	}

	// Token estimation.
	if res.Tokens.InputTokens <= 0 {
		t.Errorf("input tokens not estimated")
	}
	if res.Tokens.ExpectedOutputTokens != 800 {
		t.Errorf("expected output = %d, want 800 (MaxTokens)", res.Tokens.ExpectedOutputTokens)
	}
	if res.Features.EstimatedContextSize != res.Tokens.InputTokens {
		t.Errorf("context size %d != input tokens %d", res.Features.EstimatedContextSize, res.Tokens.InputTokens)
	}

	// Routing hints.
	if !res.Hints.HasCode || !res.Hints.MultiTurn {
		t.Errorf("hints not derived: %+v", res.Hints)
	}
	if res.Hints.ConversationTurns != 4 {
		t.Errorf("hint turns = %d, want 4", res.Hints.ConversationTurns)
	}
}

func TestEngine_AttributesMatchRoutingKeys(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), codeRequest())
	attrs := res.Attributes()

	// The token keys must match what routing's cost scorer reads, as int values.
	if v, ok := attrs[analysis.AttrEstimatedInputTokens].(int); !ok || v != res.Tokens.InputTokens {
		t.Errorf("estimated_input_tokens attr = %v, want int %d", attrs[analysis.AttrEstimatedInputTokens], res.Tokens.InputTokens)
	}
	if attrs[analysis.AttrEstimatedOutputTokens].(int) != 800 {
		t.Errorf("estimated_output_tokens attr wrong")
	}
	if attrs[analysis.AttrHasCode] != true {
		t.Errorf("has_code attr should be true")
	}
	for _, k := range []string{analysis.AttrHasMath, analysis.AttrHasStructuredData, analysis.AttrConversationTurns, analysis.AttrLongContext, analysis.AttrMultiTurn} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("attribute %q missing", k)
		}
	}
}

func TestEngine_LongContextThreshold(t *testing.T) {
	// Low threshold so a short prompt trips the long-context hint.
	e := analysis.New(analysis.WithLongContextThreshold(1))
	res := e.Analyze(context.Background(), provider.ChatRequest{Messages: []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "hello world this is long enough"},
	}})
	if !res.Hints.LongContext {
		t.Errorf("expected LongContext with threshold 1")
	}
}

func TestEngine_ContextRoundTrip(t *testing.T) {
	e := analysis.New()
	res := e.Analyze(context.Background(), codeRequest())
	ctx := analysis.NewContext(context.Background(), res)

	got, ok := analysis.FromContext(ctx)
	if !ok {
		t.Fatal("analysis result not found in context")
	}
	if got.Tokens.InputTokens != res.Tokens.InputTokens {
		t.Errorf("round-tripped result differs")
	}
	if _, ok := analysis.FromContext(context.Background()); ok {
		t.Errorf("empty context should carry no analysis")
	}
}

func TestEngine_CustomExtractor(t *testing.T) {
	// A custom extractor is appended and runs in the pipeline.
	e := analysis.New(analysis.WithExtractor(tagExtractor{}))
	res := e.Analyze(context.Background(), provider.ChatRequest{Messages: []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "URGENT: fix this"},
	}})
	// tagExtractor sets HasMath as a stand-in signal when it sees "URGENT".
	if !res.Features.HasMath {
		t.Errorf("custom extractor did not run")
	}
}

// tagExtractor is a trivial custom extractor used to prove the WithExtractor seam.
type tagExtractor struct{}

func (tagExtractor) Name() string { return "tag" }
func (tagExtractor) Extract(p analysis.Preprocessed, f *analysis.PromptFeatures) {
	if len(p.Prompt) >= 6 && p.Prompt[:6] == "URGENT" {
		f.HasMath = true
	}
}
