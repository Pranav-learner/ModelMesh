package tracing

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// DefaultServiceName labels traces from this service.
const DefaultServiceName = "modelmesh"

// Provider wraps the OTel SDK tracer provider. It is the tracing boundary: span
// exporters (OTLP, stdout, in-memory) are configured here, and the rest of the
// application receives only Tracer instances.
type Provider struct {
	tp *sdktrace.TracerProvider
}

type providerConfig struct {
	serviceName   string
	sampleRatio   float64
	syncExporter  sdktrace.SpanExporter
	batchExporter sdktrace.SpanExporter
}

// ProviderOption configures a Provider.
type ProviderOption func(*providerConfig)

// WithServiceName sets the service.name resource attribute on emitted spans.
func WithServiceName(name string) ProviderOption {
	return func(c *providerConfig) {
		if name != "" {
			c.serviceName = name
		}
	}
}

// WithSampleRatio enables head-based ratio sampling in (0,1). Values <= 0 or >= 1
// mean always-sample (the default).
func WithSampleRatio(ratio float64) ProviderOption {
	return func(c *providerConfig) { c.sampleRatio = ratio }
}

// WithBatchExporter exports spans in the background via a batch processor. This is
// the production path (e.g. an OTLP exporter).
func WithBatchExporter(exp sdktrace.SpanExporter) ProviderOption {
	return func(c *providerConfig) { c.batchExporter = exp }
}

// WithSyncExporter exports spans synchronously via a simple processor. This is the
// testing path (e.g. an in-memory exporter), so spans are visible immediately.
func WithSyncExporter(exp sdktrace.SpanExporter) ProviderOption {
	return func(c *providerConfig) { c.syncExporter = exp }
}

// NewProvider builds a tracer provider. Without an exporter, spans are still
// sampled and carry valid trace/span IDs (usable for log correlation) but are not
// shipped anywhere.
func NewProvider(opts ...ProviderOption) (*Provider, error) {
	cfg := providerConfig{serviceName: DefaultServiceName, sampleRatio: 1}
	for _, opt := range opts {
		opt(&cfg)
	}

	sampler := sdktrace.AlwaysSample()
	if cfg.sampleRatio > 0 && cfg.sampleRatio < 1 {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.sampleRatio))
	}

	res := resource.NewSchemaless(attribute.String("service.name", cfg.serviceName))

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	}
	if cfg.syncExporter != nil {
		tpOpts = append(tpOpts, sdktrace.WithSyncer(cfg.syncExporter))
	}
	if cfg.batchExporter != nil {
		tpOpts = append(tpOpts, sdktrace.WithBatcher(cfg.batchExporter))
	}

	return &Provider{tp: sdktrace.NewTracerProvider(tpOpts...)}, nil
}

// Tracer returns a named tracer.
func (p *Provider) Tracer(name string) Tracer {
	return otelTracer{tracer: p.tp.Tracer(name)}
}

// Shutdown flushes and stops the tracer provider, ensuring buffered spans are
// exported.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}
