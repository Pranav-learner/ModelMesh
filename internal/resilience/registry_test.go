package resilience_test

import (
	"context"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
	"github.com/symbiotes/modelmesh/internal/resilience"
	"github.com/symbiotes/modelmesh/internal/routing"
)

// Compile-time proof that the health Registry can be injected into the routing
// engine's health seam — without either package importing the other in non-test
// code. This is the router-integration contract.
var _ routing.HealthProvider = (*resilience.Registry)(nil)

// downProvider is an always-unhealthy provider for the external registry test.
type downProvider struct{}

func (downProvider) Name() string { return "openai" }
func (downProvider) HealthCheck(context.Context) (provider.HealthStatus, error) {
	return provider.HealthStatus{State: provider.HealthStateUnhealthy}, nil
}
func (downProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (downProvider) Embeddings(context.Context, provider.EmbeddingRequest) (provider.EmbeddingResponse, error) {
	return provider.EmbeddingResponse{}, nil
}
func (downProvider) Models(context.Context) ([]provider.ModelInfo, error) { return nil, nil }

type downSource struct{}

func (downSource) ListProviders() []string { return []string{"openai"} }
func (downSource) GetProvider(name string) (provider.LLMProvider, error) {
	return downProvider{}, nil
}

func TestRegistry_UnknownProvider(t *testing.T) {
	if _, ok := resilience.NewRegistry().Health("ghost"); ok {
		t.Errorf("Health(ghost) = ok, want not found")
	}
}

func TestRegistry_RoutingIntegration(t *testing.T) {
	reg := resilience.NewRegistry()
	breakers := resilience.NewManager(resilience.Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	mon := resilience.NewMonitor(
		resilience.MonitorConfig{Interval: time.Hour, Timeout: time.Second},
		downSource{}, breakers, reg,
	)

	mon.CheckNow(context.Background()) // one failing probe -> breaker opens

	hs, ok := reg.Health("openai")
	if !ok {
		t.Fatalf("Health(openai) not found after probe")
	}
	if hs.State != provider.HealthStateUnhealthy {
		t.Errorf("mapped state = %s, want unhealthy (open breaker)", hs.State)
	}
	if hs.Provider != "openai" {
		t.Errorf("Health provider = %q, want openai", hs.Provider)
	}
}
