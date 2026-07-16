package shadow_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/shadow"
	"github.com/symbiotes/modelmesh/internal/tracing"
)

// fixedSelector always picks a specific provider — proves WithSelector.
type fixedSelector struct{ provider string }

func (f fixedSelector) Name() string { return "fixed" }
func (f fixedSelector) Select(_ shadow.Target, candidates []shadow.Target) (shadow.Target, bool) {
	for _, c := range candidates {
		if c.Provider == f.provider {
			return c, true
		}
	}
	return shadow.Target{}, false
}

func TestManager_OptionsWiring(t *testing.T) {
	logs := &bytes.Buffer{}
	stamp := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, false), controllable("cohere", nil, nil, false))

	m, err := shadow.New(
		shadow.Config{Policy: shadow.PolicyFixedPercentage, Percentage: 100},
		pm,
		shadow.WithLogger(logger.NewWithWriter(logs, logger.LevelDebug)),
		shadow.WithSampler(alwaysSampler),
		shadow.WithSelector(fixedSelector{provider: "cohere"}),
		shadow.WithClock(func() time.Time { return stamp }),
		shadow.WithIDGenerator(func() string { return "fixed-id" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if m.Policy() != shadow.PolicyFixedPercentage {
		t.Errorf("policy = %s", m.Policy())
	}

	// Correlation ID is captured from the primary context.
	ctx := tracing.WithRequestID(context.Background(), "req_primary_123")
	exec, ok := m.Shadow(ctx, chatReq(), shadow.Primary{Target: shadow.Target{Provider: "openai", Model: "m"}})
	if !ok {
		t.Fatal("expected dispatch")
	}
	if exec.ID != "fixed-id" {
		t.Errorf("id generator not used: %s", exec.ID)
	}
	if exec.Request.Target.Provider != "cohere" {
		t.Errorf("custom selector not used: %s", exec.Request.Target.Provider)
	}
	if exec.Metadata.CorrelationID != "req_primary_123" {
		t.Errorf("correlation id = %q, want req_primary_123", exec.Metadata.CorrelationID)
	}
	if !exec.Metadata.CreatedAt.Equal(stamp) {
		t.Errorf("clock not used: %v", exec.Metadata.CreatedAt)
	}

	m.Wait()
	if !strings.Contains(logs.String(), "shadow completed") {
		t.Errorf("expected completion log, got: %s", logs.String())
	}
	if len(m.Recent()) != 1 {
		t.Errorf("recent executions = %d, want 1", len(m.Recent()))
	}
}

func TestManager_WithPolicyOverride(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false), controllable("anthropic", nil, nil, false))
	m, err := shadow.New(shadow.DefaultConfig(), pm, shadow.WithPolicy(shadow.DisabledPolicy{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Shadow(context.Background(), chatReq(), shadow.Primary{Target: shadow.Target{Provider: "openai"}}); ok {
		t.Errorf("explicit disabled policy should suppress shadowing")
	}
}

func TestManager_UnknownPolicyFailsFast(t *testing.T) {
	pm := sourceOf(t, controllable("openai", nil, nil, false))
	if _, err := shadow.New(shadow.Config{Policy: "nonsense"}, pm); err == nil {
		t.Errorf("unknown policy should fail construction")
	}
	if _, err := shadow.New(shadow.Config{Policy: shadow.PolicyRuleBased}, pm); err == nil {
		t.Errorf("reserved policy should fail construction (not implemented)")
	}
}

// ensure provider import is used even if the mock helpers move.
var _ = provider.ChatRequest{}
