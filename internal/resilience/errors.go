package resilience

import (
	"errors"
	"fmt"
)

// Resilience sentinel errors. Callers match with errors.Is.
var (
	// ErrTooManyFailures is the reason a breaker opens: the failure threshold was
	// reached. It is part of the error chain returned when a request is rejected
	// by an open circuit, so callers can distinguish "open due to failures" from
	// other open causes.
	ErrTooManyFailures = errors.New("circuit breaker: too many failures")

	// ErrCircuitOpen indicates a request was rejected because the circuit is open
	// (fast-fail). No provider call was made.
	ErrCircuitOpen = errors.New("circuit breaker: open")

	// ErrHalfOpenLimitReached indicates a request was rejected because the breaker
	// is half-open and already has the maximum number of probe requests in flight.
	ErrHalfOpenLimitReached = errors.New("circuit breaker: half-open request limit reached")

	// ErrInvalidBreakerConfig indicates a structurally invalid breaker config.
	ErrInvalidBreakerConfig = errors.New("invalid circuit breaker config")
)

// openRejection is the immutable error returned when a call is rejected by an
// open circuit. It matches both ErrCircuitOpen and ErrTooManyFailures via
// errors.Is, and is a package var to avoid per-rejection allocation.
var openRejection = fmt.Errorf("%w (%w)", ErrCircuitOpen, ErrTooManyFailures)
