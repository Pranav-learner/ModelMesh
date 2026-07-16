package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DefaultNamespace prefixes every ModelMesh metric name.
const DefaultNamespace = "modelmesh"

// Abstraction primitives. These interfaces hide the Prometheus client from the
// rest of the application. The Prometheus scalar types satisfy them structurally;
// the labeled variants are adapted by thin wrappers below.

// Counter is a monotonically increasing value.
type Counter interface {
	Inc()
	Add(float64)
}

// Gauge is a value that can go up or down.
type Gauge interface {
	Set(float64)
	Inc()
	Dec()
	Add(float64)
	Sub(float64)
}

// Histogram samples observations into buckets.
type Histogram interface {
	Observe(float64)
}

// CounterVec is a Counter partitioned by label values.
type CounterVec interface {
	With(labelValues ...string) Counter
}

// GaugeVec is a Gauge partitioned by label values.
type GaugeVec interface {
	With(labelValues ...string) Gauge
}

// HistogramVec is a Histogram partitioned by label values.
type HistogramVec interface {
	With(labelValues ...string) Histogram
}

// Manager is the centralized metrics registry. It registers metrics and serves
// them, wrapping a prometheus.Registry so callers never touch Prometheus types.
// It is intended to be constructed once during startup; registration is not
// designed for concurrent use, but recording on the returned metrics is.
type Manager struct {
	namespace string
	reg       *prometheus.Registry
}

// Option configures a Manager.
type Option func(*Manager)

// WithNamespace overrides the metric name prefix (default "modelmesh").
func WithNamespace(ns string) Option {
	return func(m *Manager) { m.namespace = ns }
}

// NewManager creates a metrics manager with its own registry.
func NewManager(opts ...Option) *Manager {
	m := &Manager{namespace: DefaultNamespace, reg: prometheus.NewRegistry()}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Handler returns the HTTP handler that serves metrics in the Prometheus text
// exposition format. A future HTTP server mounts it at /metrics.
func (m *Manager) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Registry returns the underlying Prometheus registry (for testing/gathering).
func (m *Manager) Registry() *prometheus.Registry { return m.reg }

// Counter registers and returns a counter.
func (m *Manager) Counter(name, help string) Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: m.namespace, Name: name, Help: help})
	m.reg.MustRegister(c)
	return c
}

// CounterVec registers and returns a labeled counter.
func (m *Manager) CounterVec(name, help string, labels []string) CounterVec {
	v := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: m.namespace, Name: name, Help: help}, labels)
	m.reg.MustRegister(v)
	return counterVec{v}
}

// Gauge registers and returns a gauge.
func (m *Manager) Gauge(name, help string) Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: m.namespace, Name: name, Help: help})
	m.reg.MustRegister(g)
	return g
}

// GaugeVec registers and returns a labeled gauge.
func (m *Manager) GaugeVec(name, help string, labels []string) GaugeVec {
	v := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: m.namespace, Name: name, Help: help}, labels)
	m.reg.MustRegister(v)
	return gaugeVec{v}
}

// Histogram registers and returns a histogram. Nil buckets use the default
// (duration-oriented) buckets.
func (m *Manager) Histogram(name, help string, buckets []float64) Histogram {
	if buckets == nil {
		buckets = prometheus.DefBuckets
	}
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: m.namespace, Name: name, Help: help, Buckets: buckets})
	m.reg.MustRegister(h)
	return h
}

// HistogramVec registers and returns a labeled histogram.
func (m *Manager) HistogramVec(name, help string, labels []string, buckets []float64) HistogramVec {
	if buckets == nil {
		buckets = prometheus.DefBuckets
	}
	v := prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: m.namespace, Name: name, Help: help, Buckets: buckets}, labels)
	m.reg.MustRegister(v)
	return histogramVec{v}
}

// --- labeled adapters: map With(...string) onto Prometheus WithLabelValues ---

type counterVec struct{ v *prometheus.CounterVec }

func (c counterVec) With(lvs ...string) Counter { return c.v.WithLabelValues(lvs...) }

type gaugeVec struct{ v *prometheus.GaugeVec }

func (g gaugeVec) With(lvs ...string) Gauge { return g.v.WithLabelValues(lvs...) }

type histogramVec struct{ v *prometheus.HistogramVec }

func (h histogramVec) With(lvs ...string) Histogram { return h.v.WithLabelValues(lvs...) }
