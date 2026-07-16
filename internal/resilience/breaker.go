package resilience

import (
	"context"
	"sync"
	"time"
)

// CircuitBreaker is the provider-independent resilience contract. It guards an
// arbitrary operation, tracking failures and transitioning between Closed, Open,
// and Half-Open per its configuration.
type CircuitBreaker interface {
	// Execute runs fn if the breaker permits, recording the outcome. If the
	// circuit is open it returns ErrCircuitOpen without calling fn; if half-open
	// and at its probe limit it returns ErrHalfOpenLimitReached. Otherwise it
	// returns fn's error unchanged.
	Execute(ctx context.Context, fn func(ctx context.Context) error) error
	// State returns the current state, applying any pending time-based transition
	// (Open -> Half-Open after the cooldown).
	State() State
	// Reset forces the breaker back to Closed and clears all counters.
	Reset()
	// RecordSuccess reports a successful outcome for a call the caller guarded
	// manually (i.e. not via Execute).
	RecordSuccess()
	// RecordFailure reports a failed outcome for a manually-guarded call.
	RecordFailure()
}

// Compile-time assertion.
var _ CircuitBreaker = (*Breaker)(nil)

// Breaker is the default CircuitBreaker implementation: a mutex-guarded state
// machine. A generation counter, bumped on every transition, lets in-flight
// Execute outcomes be discarded if the state changed underneath them.
type Breaker struct {
	cfg   Config
	clock func() time.Time

	mu                  sync.Mutex
	state               State
	generation          uint64
	consecutiveFailures int       // Closed
	halfOpenSuccesses   int       // Half-Open
	halfOpenInFlight    int       // Half-Open admitted-but-not-completed probes
	openedAt            time.Time // when the breaker last opened
}

// Option configures a Breaker.
type Option func(*Breaker)

// WithClock injects a time source for deterministic transition tests.
func WithClock(now func() time.Time) Option {
	return func(b *Breaker) {
		if now != nil {
			b.clock = now
		}
	}
}

// NewBreaker constructs a breaker from cfg (defaults applied), starting Closed.
func NewBreaker(cfg Config, opts ...Option) *Breaker {
	b := &Breaker{
		cfg:   cfg.WithDefaults(),
		clock: time.Now,
		state: StateClosed,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Execute runs fn under the breaker's protection.
func (b *Breaker) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	generation, admitted, err := b.beforeRequest()
	if err != nil {
		return err
	}
	runErr := fn(ctx)
	b.afterRequest(generation, admitted, b.succeeded(runErr))
	return runErr
}

// State returns the current state after applying any pending time transition.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyTimeTransition(b.clock())
	return b.state
}

// Reset forces the breaker Closed and clears counters.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setState(StateClosed, b.clock())
}

// RecordSuccess records a manual success against the current state.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	b.applyTimeTransition(now)
	b.onSuccess(now)
}

// RecordFailure records a manual failure against the current state.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	b.applyTimeTransition(now)
	b.onFailure(now)
}

// Counts is a snapshot of a breaker's state and counters, for diagnostics.
type Counts struct {
	State               State
	ConsecutiveFailures int
	HalfOpenSuccesses   int
	HalfOpenInFlight    int
	OpenedAt            time.Time
}

// Counts returns a snapshot of the breaker's current state and counters.
func (b *Breaker) Counts() Counts {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyTimeTransition(b.clock())
	return Counts{
		State:               b.state,
		ConsecutiveFailures: b.consecutiveFailures,
		HalfOpenSuccesses:   b.halfOpenSuccesses,
		HalfOpenInFlight:    b.halfOpenInFlight,
		OpenedAt:            b.openedAt,
	}
}

// beforeRequest checks whether a request may proceed. It returns the current
// generation, whether a half-open probe slot was taken, or a rejection error.
func (b *Breaker) beforeRequest() (generation uint64, admitted bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyTimeTransition(b.clock())

	switch b.state {
	case StateOpen:
		return 0, false, openRejection
	case StateHalfOpen:
		if b.halfOpenInFlight >= b.cfg.HalfOpenMaxRequests {
			return 0, false, ErrHalfOpenLimitReached
		}
		b.halfOpenInFlight++
		return b.generation, true, nil
	default: // StateClosed
		return b.generation, false, nil
	}
}

// afterRequest records the outcome of an Execute call. A half-open probe slot is
// released; a stale outcome (state changed during the call) is ignored, because
// the transition already reset the counters.
func (b *Breaker) afterRequest(generation uint64, admitted bool, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if admitted && generation == b.generation && b.state == StateHalfOpen {
		b.halfOpenInFlight--
	}
	if generation != b.generation {
		return // stale outcome from a superseded generation
	}
	now := b.clock()
	if success {
		b.onSuccess(now)
	} else {
		b.onFailure(now)
	}
}

// onSuccess applies a success to the state machine. Caller holds the lock.
func (b *Breaker) onSuccess(now time.Time) {
	switch b.state {
	case StateClosed:
		b.consecutiveFailures = 0
	case StateHalfOpen:
		b.halfOpenSuccesses++
		if b.halfOpenSuccesses >= b.cfg.SuccessThreshold {
			b.setState(StateClosed, now)
		}
	}
}

// onFailure applies a failure to the state machine. Caller holds the lock.
func (b *Breaker) onFailure(now time.Time) {
	switch b.state {
	case StateClosed:
		b.consecutiveFailures++
		if b.consecutiveFailures >= b.cfg.FailureThreshold {
			b.setState(StateOpen, now)
		}
	case StateHalfOpen:
		b.setState(StateOpen, now)
	}
}

// applyTimeTransition performs the lazy Open -> Half-Open transition once the
// cooldown has elapsed. Caller holds the lock.
func (b *Breaker) applyTimeTransition(now time.Time) {
	if b.state == StateOpen && now.Sub(b.openedAt) >= b.cfg.OpenTimeout {
		b.setState(StateHalfOpen, now)
	}
}

// setState transitions to s, bumping the generation and clearing counters. Caller
// holds the lock. It is only called on genuine transitions.
func (b *Breaker) setState(s State, now time.Time) {
	b.state = s
	b.generation++
	b.consecutiveFailures = 0
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
	if s == StateOpen {
		b.openedAt = now
	}
}

// succeeded reports whether an operation error counts as a success.
func (b *Breaker) succeeded(err error) bool {
	if b.cfg.IsFailure != nil {
		return !b.cfg.IsFailure(err)
	}
	return err == nil
}
