package evaluation_test

import (
	"context"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func resp(prov, model, content string, tokens int) provider.ChatResponse {
	return provider.ChatResponse{
		Provider: prov, Model: model,
		Usage: provider.Usage{PromptTokens: tokens / 2, CompletionTokens: tokens / 2, TotalTokens: tokens},
		Choices: []provider.Choice{{
			Message:      provider.ChatMessage{Role: provider.RoleAssistant, Content: content},
			FinishReason: provider.FinishReasonStop,
		}},
	}
}

func side(prov, model, content string, tokens int, latency time.Duration) evaluation.Side {
	return evaluation.Side{Provider: prov, Model: model, Response: resp(prov, model, content, tokens), Latency: latency}
}

// perModelCost prices "expensive" 10x "cheap".
func perModelCost() evaluation.CostModel {
	return evaluation.CostModelFunc(func(model string, usage provider.Usage) float64 {
		rate := 0.001
		if model == "expensive" {
			rate = 0.01
		}
		return float64(usage.TotalTokens) * rate
	})
}

// --- Response comparison ---

func TestCompare_ResponseComparison(t *testing.T) {
	e := evaluation.New()

	// Identical responses.
	same := e.Compare(
		side("openai", "gpt-4", "the answer is 42", 10, time.Second),
		side("anthropic", "claude", "the answer is 42", 10, time.Second),
	)
	if !same.Quality.ExactMatch || same.Quality.TextSimilarity != 1 {
		t.Errorf("identical responses: exact=%v sim=%v", same.Quality.ExactMatch, same.Quality.TextSimilarity)
	}
	if !same.Quality.FinishReasonMatch || same.Quality.LengthDifference != 0 {
		t.Errorf("identical responses should match finish reason and length: %+v", same.Quality)
	}

	// Different responses.
	diff := e.Compare(
		side("openai", "gpt-4", "the answer is 42", 10, time.Second),
		side("anthropic", "claude", "the answer is definitely 43 today", 10, time.Second),
	)
	if diff.Quality.ExactMatch {
		t.Errorf("different responses should not exact-match")
	}
	if diff.Quality.TextSimilarity <= 0 || diff.Quality.TextSimilarity >= 1 {
		t.Errorf("partial overlap similarity should be in (0,1), got %v", diff.Quality.TextSimilarity)
	}
	if diff.Quality.LengthDifference <= 0 {
		t.Errorf("shadow is longer; length difference should be positive, got %d", diff.Quality.LengthDifference)
	}
}

// --- Cost comparison ---

func TestCompare_CostComparison(t *testing.T) {
	e := evaluation.New(evaluation.WithCostModel(perModelCost()))
	// Primary uses the expensive model (1000 tokens), shadow the cheap one (1000).
	c := e.Compare(
		side("openai", "expensive", "answer", 1000, time.Second),
		side("anthropic", "cheap", "answer", 1000, time.Second),
	)
	// expensive: 1000*0.01 = 10; cheap: 1000*0.001 = 1 → shadow cheaper by 9.
	if c.Cost.PrimaryCost != 10 || c.Cost.ShadowCost != 1 {
		t.Errorf("costs = %v/%v, want 10/1", c.Cost.PrimaryCost, c.Cost.ShadowCost)
	}
	if !c.Cost.ShadowCheaper || c.Cost.Difference != -9 {
		t.Errorf("shadow should be cheaper by 9, got difference %v", c.Cost.Difference)
	}
	if c.Cost.TokenDifference != 0 {
		t.Errorf("equal tokens → 0 token difference, got %d", c.Cost.TokenDifference)
	}
}

func TestCompare_TokenDifference(t *testing.T) {
	e := evaluation.New()
	c := e.Compare(
		side("openai", "gpt-4", "short", 100, time.Second),
		side("anthropic", "claude", "much longer response text here", 250, time.Second),
	)
	if c.Cost.TokenDifference != 150 {
		t.Errorf("token difference = %d, want 150", c.Cost.TokenDifference)
	}
}

// --- Latency comparison ---

func TestCompare_LatencyComparison(t *testing.T) {
	e := evaluation.New()
	c := e.Compare(
		side("openai", "gpt-4", "answer", 10, 800*time.Millisecond),
		side("anthropic", "claude", "answer", 10, 200*time.Millisecond),
	)
	if !c.Latency.ShadowFaster {
		t.Errorf("shadow should be faster")
	}
	if c.Latency.Difference != -600*time.Millisecond {
		t.Errorf("latency difference = %v, want -600ms", c.Latency.Difference)
	}
}

// --- Winner ---

func TestCompare_Winner(t *testing.T) {
	e := evaluation.New(evaluation.WithCostModel(perModelCost()))

	// Shadow cheaper + faster → shadow wins.
	sw := e.Compare(
		side("openai", "expensive", "a", 100, 900*time.Millisecond),
		side("anthropic", "cheap", "a", 100, 100*time.Millisecond),
	)
	if sw.Winner != evaluation.WinnerShadow {
		t.Errorf("winner = %s, want shadow", sw.Winner)
	}

	// Primary cheaper + faster → primary wins.
	pw := e.Compare(
		side("openai", "cheap", "a", 100, 100*time.Millisecond),
		side("anthropic", "expensive", "a", 100, 900*time.Millisecond),
	)
	if pw.Winner != evaluation.WinnerPrimary {
		t.Errorf("winner = %s, want primary", pw.Winner)
	}

	// Cheaper but slower (one each) → tie.
	tie := e.Compare(
		side("openai", "expensive", "a", 100, 100*time.Millisecond), // slower? no, faster
		side("anthropic", "cheap", "a", 100, 900*time.Millisecond),  // cheaper but slower
	)
	if tie.Winner != evaluation.WinnerTie {
		t.Errorf("winner = %s, want tie", tie.Winner)
	}
}

// --- Embedding abstraction (optional) ---

func TestCompare_EmbeddingSimilarityOptional(t *testing.T) {
	// Default: no embedding scorer → not computed.
	base := evaluation.New().Compare(
		side("a", "m", "x", 1, time.Second), side("b", "m", "y", 1, time.Second))
	if base.Quality.HasEmbedding {
		t.Errorf("no embedding scorer should leave HasEmbedding false")
	}

	// Injected abstraction is used.
	e := evaluation.New(evaluation.WithEmbeddingSimilarity(func(_, _ string) (float64, bool) { return 0.87, true }))
	c := e.Compare(side("a", "m", "x", 1, time.Second), side("b", "m", "y", 1, time.Second))
	if !c.Quality.HasEmbedding || c.Quality.EmbeddingSimilarity != 0.87 {
		t.Errorf("embedding similarity = %v (has=%v), want 0.87", c.Quality.EmbeddingSimilarity, c.Quality.HasEmbedding)
	}
}

// --- Evaluate via the shadow seam ---

func TestEvaluate_StoresSuccessfulComparison(t *testing.T) {
	e := evaluation.New(evaluation.WithCostModel(perModelCost()))
	comp := shadow.Comparison{
		CorrelationID:   "req_1",
		Primary:         shadow.Target{Provider: "openai", Model: "expensive"},
		Shadow:          shadow.Target{Provider: "anthropic", Model: "cheap"},
		PrimaryResponse: resp("openai", "expensive", "hello", 100),
		PrimaryLatency:  500 * time.Millisecond,
		ShadowResult: shadow.ShadowResult{
			Success:  true,
			Response: resp("anthropic", "cheap", "hello", 100),
			Latency:  200 * time.Millisecond,
		},
	}
	e.Evaluate(context.Background(), comp)

	records := e.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	r := records[0]
	if !r.Comparable || r.CorrelationID != "req_1" {
		t.Errorf("record = %+v", r)
	}
	if !r.Comparison.Quality.ExactMatch || !r.Comparison.Cost.ShadowCheaper || !r.Comparison.Latency.ShadowFaster {
		t.Errorf("comparison metrics wrong: %+v", r.Comparison)
	}
}

func TestEvaluate_RecordsFailedShadow(t *testing.T) {
	e := evaluation.New()
	e.Evaluate(context.Background(), shadow.Comparison{
		CorrelationID: "req_2",
		Primary:       shadow.Target{Provider: "openai", Model: "gpt-4"},
		Shadow:        shadow.Target{Provider: "anthropic", Model: "claude"},
		ShadowResult:  shadow.ShadowResult{Success: false, Err: "provider timeout"},
	})
	records := e.Records()
	if len(records) != 1 || records[0].Comparable {
		t.Fatalf("failed shadow should store a non-comparable record: %+v", records)
	}
	if records[0].ShadowError != "provider timeout" {
		t.Errorf("shadow error = %q", records[0].ShadowError)
	}
}
