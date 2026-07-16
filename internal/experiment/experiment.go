package experiment

import (
	"time"

	"github.com/symbiotes/modelmesh/internal/adaptive"
	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/shadow"
)

// SavingsFunc reports a running savings figure (USD).
type SavingsFunc func() float64

// UsageFunc reports per-provider request counts.
type UsageFunc func() map[string]int

// Experiment is a named comparison campaign: it bundles an evaluation engine
// (required) with optional live telemetry sources and produces a Report on demand.
type Experiment struct {
	name        string
	description string
	createdAt   time.Time

	eval           *evaluation.Engine
	shadow         *shadow.Manager
	classification *adaptive.Collector
	cacheSavings   SavingsFunc
	budgetSavings  SavingsFunc
	usage          UsageFunc
	monthlyFactor  float64
	clock          func() time.Time
}

// ExperimentOption configures an Experiment.
type ExperimentOption func(*Experiment)

// WithShadowManager attaches the shadow manager driving the experiment (for
// sampling statistics).
func WithShadowManager(m *shadow.Manager) ExperimentOption {
	return func(e *Experiment) {
		if m != nil {
			e.shadow = m
		}
	}
}

// WithClassification attaches the adaptive metrics collector (for classification
// distribution and average complexity).
func WithClassification(c *adaptive.Collector) ExperimentOption {
	return func(e *Experiment) {
		if c != nil {
			e.classification = c
		}
	}
}

// WithCacheSavings attaches a running cache-savings source (USD).
func WithCacheSavings(f SavingsFunc) ExperimentOption {
	return func(e *Experiment) {
		if f != nil {
			e.cacheSavings = f
		}
	}
}

// WithBudgetSavings attaches a running budget-savings source (USD).
func WithBudgetSavings(f SavingsFunc) ExperimentOption {
	return func(e *Experiment) {
		if f != nil {
			e.budgetSavings = f
		}
	}
}

// WithProviderUsage attaches a per-provider usage source.
func WithProviderUsage(f UsageFunc) ExperimentOption {
	return func(e *Experiment) {
		if f != nil {
			e.usage = f
		}
	}
}

// WithMonthlyFactor sets the observed→monthly savings projection multiplier.
func WithMonthlyFactor(factor float64) ExperimentOption {
	return func(e *Experiment) {
		if factor > 0 {
			e.monthlyFactor = factor
		}
	}
}

// Name returns the experiment name.
func (e *Experiment) Name() string { return e.name }

// Description returns the experiment description.
func (e *Experiment) Description() string { return e.description }

// CreatedAt returns when the experiment was created.
func (e *Experiment) CreatedAt() time.Time { return e.createdAt }

// Records returns the experiment's stored evaluation records.
func (e *Experiment) Records() []evaluation.EvaluationRecord { return e.eval.Records() }

// Report gathers the current telemetry and builds the analytics report.
func (e *Experiment) Report() Report {
	in := Inputs{
		Experiment:    e.name,
		Description:   e.description,
		GeneratedAt:   e.clock(),
		Evaluation:    e.eval.Statistics(),
		MonthlyFactor: e.monthlyFactor,
	}
	if e.shadow != nil {
		in.Shadow = e.shadow.Stats()
	}
	if e.classification != nil {
		in.Classification = e.classification.Snapshot()
	}
	if e.usage != nil {
		in.ProviderUsage = e.usage()
	}
	if e.cacheSavings != nil {
		in.CacheSavingsUSD = e.cacheSavings()
	}
	if e.budgetSavings != nil {
		in.BudgetSavingsUSD = e.budgetSavings()
	}
	return BuildReport(in)
}

// Stop drains any in-flight shadow traffic for the experiment. It is safe to call
// when no shadow manager is attached.
func (e *Experiment) Stop() {
	if e.shadow != nil {
		e.shadow.Wait()
	}
}
