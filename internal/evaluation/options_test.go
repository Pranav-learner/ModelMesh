package evaluation_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

func TestEngine_OptionsWiring(t *testing.T) {
	logs := &bytes.Buffer{}
	stamp := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	e := evaluation.New(
		evaluation.WithTextSimilarity(func(a, b string) float64 {
			if a == b {
				return 0.5 // deliberately non-standard, to prove the override is used
			}
			return 0.0
		}),
		evaluation.WithLogger(logger.NewWithWriter(logs, logger.LevelDebug)),
		evaluation.WithClock(func() time.Time { return stamp }),
		evaluation.WithIDGenerator(func() string { return "eval-fixed" }),
		evaluation.WithMaxRecords(2),
	)

	e.Evaluate(context.Background(), comparison("r", "m", "m", "same", "same", 10, time.Second, time.Second))
	records := e.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	r := records[0]
	if r.ID != "eval-fixed" {
		t.Errorf("id generator not used: %s", r.ID)
	}
	if !r.Timestamp.Equal(stamp) {
		t.Errorf("clock not used: %v", r.Timestamp)
	}
	if r.Comparison.Quality.TextSimilarity != 0.5 {
		t.Errorf("custom similarity not used: %v", r.Comparison.Quality.TextSimilarity)
	}
	if !strings.Contains(logs.String(), "shadow evaluated") {
		t.Errorf("expected evaluation log, got: %s", logs.String())
	}
}

func TestEngine_MaxRecordsBounded(t *testing.T) {
	e := evaluation.New(evaluation.WithMaxRecords(3))
	for i := 0; i < 10; i++ {
		e.Evaluate(context.Background(), comparison("r", "m", "m", "a", "b", 1, time.Second, time.Second))
	}
	if n := len(e.Records()); n != 3 {
		t.Errorf("bounded records = %d, want 3", n)
	}
}

func TestEngine_EmptyResponseText(t *testing.T) {
	// A response with no choices is handled (empty text, empty finish reason).
	e := evaluation.New()
	c := e.Compare(
		evaluation.Side{Provider: "a", Model: "m", Response: provider.ChatResponse{}, Latency: time.Second},
		evaluation.Side{Provider: "b", Model: "m", Response: provider.ChatResponse{}, Latency: time.Second},
	)
	if !c.Quality.ExactMatch || c.Quality.PrimaryLength != 0 {
		t.Errorf("empty responses should exact-match with zero length: %+v", c.Quality)
	}
	if c.Quality.PrimaryFinishReason != "" {
		t.Errorf("empty response should have empty finish reason")
	}
}

var _ shadow.Evaluator = evaluation.New()
