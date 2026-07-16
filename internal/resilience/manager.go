package resilience

import (
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/logger"
)

// Manager maintains an independent circuit breaker per provider. Each provider's
// breaker is created on first access with the manager's configuration, so one
// provider's failures never affect another's breaker. The Manager is safe for
// concurrent use.
type Manager struct {
	cfg   Config
	clock func() time.Time
	log   logger.Logger

	mu       sync.Mutex
	breakers map[string]*Breaker
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithManagerClock injects a time source shared by all created breakers, for
// deterministic tests.
func WithManagerClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.clock = now
		}
	}
}

// WithLogger injects a structured logger.
func WithLogger(l logger.Logger) ManagerOption {
	return func(m *Manager) {
		if l != nil {
			m.log = l
		}
	}
}

// NewManager constructs a breaker Manager. The configuration's defaults are
// applied; each per-provider breaker is created from it.
func NewManager(cfg Config, opts ...ManagerOption) *Manager {
	m := &Manager{
		cfg:      cfg.WithDefaults(),
		clock:    time.Now,
		log:      logger.Nop(),
		breakers: make(map[string]*Breaker),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Breaker returns the circuit breaker for a provider, creating it on first use.
func (m *Manager) Breaker(providerName string) *Breaker {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.breakers[providerName]; ok {
		return b
	}
	b := NewBreaker(m.cfg, WithClock(m.clock))
	m.breakers[providerName] = b
	m.log.Debug("created circuit breaker", logger.String("provider", providerName))
	return b
}

// State returns the current state of a provider's breaker, creating the breaker
// if it does not yet exist (a fresh breaker is Closed).
func (m *Manager) State(providerName string) State {
	return m.Breaker(providerName).State()
}

// States returns a snapshot of every known provider's breaker state.
func (m *Manager) States() map[string]State {
	m.mu.Lock()
	breakers := make(map[string]*Breaker, len(m.breakers))
	for name, b := range m.breakers {
		breakers[name] = b
	}
	m.mu.Unlock()

	out := make(map[string]State, len(breakers))
	for name, b := range breakers {
		out[name] = b.State()
	}
	return out
}

// Reset forces a provider's breaker back to Closed.
func (m *Manager) Reset(providerName string) {
	m.Breaker(providerName).Reset()
}

// ResetAll forces every known breaker back to Closed.
func (m *Manager) ResetAll() {
	m.mu.Lock()
	breakers := make([]*Breaker, 0, len(m.breakers))
	for _, b := range m.breakers {
		breakers = append(breakers, b)
	}
	m.mu.Unlock()

	for _, b := range breakers {
		b.Reset()
	}
}
