package analysis_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestExtractorNames(t *testing.T) {
	names := map[string]bool{}
	for _, x := range analysis.DefaultExtractors() {
		names[x.Name()] = true
	}
	for _, want := range []string{"length", "code", "math", "structured_data"} {
		if !names[want] {
			t.Errorf("default extractors missing %q", want)
		}
	}
}

func TestEngine_AllOptionsWiring(t *testing.T) {
	logs := &bytes.Buffer{}
	// Replace preprocessor, extractor set, token estimator, and logger.
	e := analysis.New(
		analysis.WithPreprocessor(analysis.NewPreprocessor(analysis.WithMaxBlankLines(0))),
		analysis.WithExtractors(analysis.LengthExtractor{}),
		analysis.WithTokenEstimator(analysis.NewHeuristicEstimator(analysis.WithCharsPerToken(2))),
		analysis.WithLogger(logger.NewWithWriter(logs, logger.LevelDebug)),
	)
	res := e.Analyze(context.Background(), provider.ChatRequest{Messages: []provider.ChatMessage{
		{Role: provider.RoleUser, Content: "abcd func"}, // only length extractor runs → no code detection
	}})
	if res.Features.HasCode {
		t.Errorf("code extractor was replaced out; HasCode should be false")
	}
	if res.Tokens.InputTokens == 0 {
		t.Errorf("custom estimator should still produce tokens")
	}
	if !strings.Contains(logs.String(), "request analyzed") {
		t.Errorf("expected analysis log, got: %s", logs.String())
	}
}
