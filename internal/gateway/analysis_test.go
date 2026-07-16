package gateway_test

import (
	"context"
	"sync"
	"testing"

	"github.com/symbiotes/modelmesh/internal/analysis"
	"github.com/symbiotes/modelmesh/internal/cache"
	"github.com/symbiotes/modelmesh/internal/gateway"
	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// capturingRouter wraps a real router and records the attributes of the routing
// context it is handed, so a test can assert the analysis enrichment reached it.
type capturingRouter struct {
	inner interface {
		Select(context.Context, routing.RoutingContext) (*routing.Selection, error)
		Route(context.Context, routing.RoutingContext) (routing.RoutingDecision, error)
	}
	mu        sync.Mutex
	lastAttrs map[string]any
}

func (c *capturingRouter) Select(ctx context.Context, rc routing.RoutingContext) (*routing.Selection, error) {
	c.mu.Lock()
	c.lastAttrs = rc.Attributes
	c.mu.Unlock()
	return c.inner.Select(ctx, rc)
}

func (c *capturingRouter) Route(ctx context.Context, rc routing.RoutingContext) (routing.RoutingDecision, error) {
	return c.inner.Route(ctx, rc)
}

func (c *capturingRouter) attrs() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastAttrs
}

func TestGatewayAnalysis_EnrichesRoutingContext(t *testing.T) {
	oa := optProvider("openai", "gpt-4")
	reg := provider.NewRegistry()
	if err := reg.Register(oa); err != nil {
		t.Fatal(err)
	}
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	manager, err := routing.Build(pm, routing.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	capR := &capturingRouter{inner: manager}

	gw := gateway.New(capR, cache.NewManager(nil), cache.Config{Enabled: false}, gateway.WithAnalyzer(analysis.New()))

	res, err := gw.Chat(context.Background(), provider.ChatRequest{
		Model:     "gpt-4",
		MaxTokens: 500,
		Messages: []provider.ChatMessage{
			{Role: provider.RoleUser, Content: "```go\nfunc add(a, b int) int { return a + b }\n```\nExplain."},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	// Every request now carries a structured analysis result.
	if res.Analysis == nil {
		t.Fatal("expected analysis result on ChatResult")
	}
	if !res.Analysis.Features.HasCode {
		t.Errorf("analysis should detect code")
	}
	if res.Analysis.Tokens.ExpectedOutputTokens != 500 {
		t.Errorf("expected output tokens = %d, want 500", res.Analysis.Tokens.ExpectedOutputTokens)
	}

	// The routing context the router saw was enriched with the analysis hints.
	attrs := capR.attrs()
	if attrs == nil {
		t.Fatal("router received no attributes")
	}
	if v, ok := attrs[analysis.AttrEstimatedInputTokens].(int); !ok || v <= 0 {
		t.Errorf("routing context missing estimated_input_tokens: %v", attrs[analysis.AttrEstimatedInputTokens])
	}
	if attrs[analysis.AttrHasCode] != true {
		t.Errorf("routing context has_code = %v, want true", attrs[analysis.AttrHasCode])
	}
	if attrs[analysis.AttrEstimatedOutputTokens].(int) != 500 {
		t.Errorf("routing context estimated_output_tokens wrong")
	}
}

func TestGatewayAnalysis_DisabledByDefault(t *testing.T) {
	oa := optProvider("openai", "gpt-4")
	reg := provider.NewRegistry()
	_ = reg.Register(oa)
	pm := provider.NewManager(reg, provider.WithDefaultProvider("openai"))
	manager, _ := routing.Build(pm, routing.DefaultConfig())

	gw := gateway.New(manager, cache.NewManager(nil), cache.Config{Enabled: false}) // no analyzer
	res, err := gw.Chat(context.Background(), provider.ChatRequest{
		Model:    "gpt-4",
		Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analysis != nil {
		t.Errorf("analysis should be nil when analyzer is not wired")
	}
}
