package experiment

import (
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/evaluation"
	"github.com/symbiotes/modelmesh/internal/logger"
)

// Manager owns named experiments. It is the entry point of the experimentation
// platform and is safe for concurrent use.
type Manager struct {
	mu          sync.Mutex
	experiments map[string]*Experiment
	order       []string
	log         logger.Logger
	clock       func() time.Time
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithLogger injects a structured logger. A nil logger is ignored.
func WithLogger(l logger.Logger) ManagerOption {
	return func(m *Manager) {
		if l != nil {
			m.log = l
		}
	}
}

// WithClock injects a time source, for deterministic timestamps in tests.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// NewManager constructs an experiment Manager.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		experiments: make(map[string]*Experiment),
		log:         logger.Nop(),
		clock:       time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Create registers a new experiment backed by the given evaluation engine. It
// fails on a duplicate name, an empty name, or a nil engine.
func (m *Manager) Create(name, description string, eval *evaluation.Engine, opts ...ExperimentOption) (*Experiment, error) {
	if name == "" || eval == nil {
		return nil, ErrInvalidExperiment
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.experiments[name]; ok {
		return nil, ErrExperimentExists
	}
	e := &Experiment{
		name:        name,
		description: description,
		createdAt:   m.clock(),
		eval:        eval,
		clock:       m.clock,
	}
	for _, opt := range opts {
		opt(e)
	}
	m.experiments[name] = e
	m.order = append(m.order, name)
	m.log.Info("experiment created", logger.String("experiment", name))
	return e, nil
}

// Get returns a registered experiment by name.
func (m *Manager) Get(name string) (*Experiment, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.experiments[name]
	return e, ok
}

// List returns the registered experiments in creation order.
func (m *Manager) List() []*Experiment {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Experiment, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.experiments[name])
	}
	return out
}

// Report returns the report for a named experiment.
func (m *Manager) Report(name string) (Report, error) {
	e, ok := m.Get(name)
	if !ok {
		return Report{}, ErrExperimentNotFound
	}
	return e.Report(), nil
}

// Reports returns the reports for every experiment, in creation order.
func (m *Manager) Reports() []Report {
	experiments := m.List()
	out := make([]Report, 0, len(experiments))
	for _, e := range experiments {
		out = append(out, e.Report())
	}
	return out
}

// StopAll drains in-flight shadow traffic across every experiment.
func (m *Manager) StopAll() {
	for _, e := range m.List() {
		e.Stop()
	}
}
