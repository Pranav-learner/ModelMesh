package resilience

import (
	"context"
	"errors"
	"fmt"

	"github.com/symbiotes/modelmesh/internal/logger"
	"github.com/symbiotes/modelmesh/internal/retry"
)

// Target is one candidate in a failover attempt: a provider and the model to use.
// It is provider-agnostic (plain strings), so the failover executor stays
// decoupled from the routing and provider packages.
type Target struct {
	Provider string
	Model    string
}

// AttemptResult records what happened for one candidate during failover.
type AttemptResult struct {
	Target  Target
	Skipped bool   // true if the candidate was skipped (open circuit / rejected)
	Reason  string // skip reason or error summary
	Err     error  // execution error, if the candidate was attempted and failed
}

// FailoverOutcome summarizes a failover attempt for diagnostics.
type FailoverOutcome struct {
	Served       Target // the candidate that succeeded (zero value if none)
	Succeeded    bool
	FailoverUsed bool // true if the served candidate was not the first-ranked one
	Attempts     []AttemptResult
}

// Failover runs an operation against ranked candidates, guarded by each
// provider's circuit breaker with optional retry, skipping candidates whose
// breaker is open, until one succeeds. It composes the breaker (admission) with
// retry (transient blips within an admitted call): the breaker is outermost, so
// retries never amplify its failure count — a whole retry sequence is one breaker
// outcome.
type Failover struct {
	breakers     *Manager
	retry        retry.Policy
	retryable    func(error) bool // retry predicate within a single provider
	failoverable func(error) bool // whether an error should trigger failover
	log          logger.Logger
}

// FailoverOption configures a Failover.
type FailoverOption func(*Failover)

// WithRetryPolicy sets the per-provider retry policy (default: no retries).
func WithRetryPolicy(p retry.Policy) FailoverOption {
	return func(f *Failover) { f.retry = p }
}

// WithRetryable sets the predicate deciding which errors are retried within a
// single provider (default: none).
func WithRetryable(pred func(error) bool) FailoverOption {
	return func(f *Failover) { f.retryable = pred }
}

// WithFailoverable sets the predicate deciding which errors trigger failover to
// the next candidate. When it returns false the error is returned immediately
// (e.g. caller-fault errors should not fail over). Default: fail over on any
// error.
func WithFailoverable(pred func(error) bool) FailoverOption {
	return func(f *Failover) { f.failoverable = pred }
}

// WithFailoverLogger injects a structured logger.
func WithFailoverLogger(l logger.Logger) FailoverOption {
	return func(f *Failover) {
		if l != nil {
			f.log = l
		}
	}
}

// NewFailover constructs a failover executor over the shared breaker manager.
func NewFailover(breakers *Manager, opts ...FailoverOption) *Failover {
	f := &Failover{breakers: breakers, log: logger.Nop()}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Do runs op against each target in rank order. For each candidate it consults
// the breaker (skipping open ones), then executes op guarded by the breaker with
// retry. It returns as soon as a candidate succeeds; if all candidates are
// skipped or fail it returns ErrAllProvidersFailed. A non-failoverable error
// short-circuits and is returned immediately.
func (f *Failover) Do(ctx context.Context, targets []Target, op func(ctx context.Context, t Target) error) (FailoverOutcome, error) {
	var outcome FailoverOutcome
	var lastErr error

	for i, target := range targets {
		breaker := f.breakers.Breaker(target.Provider)
		if breaker.State() == StateOpen {
			outcome.Attempts = append(outcome.Attempts, AttemptResult{Target: target, Skipped: true, Reason: "circuit open"})
			f.log.Debug("failover: skipping provider with open circuit", logger.String("provider", target.Provider))
			continue
		}

		execErr := breaker.Execute(ctx, func(cctx context.Context) error {
			return retry.Do(cctx, f.retry, func(rctx context.Context) error {
				return op(rctx, target)
			}, f.retryable)
		})

		// A breaker rejection means the candidate is unavailable: skip to the next.
		if errors.Is(execErr, ErrCircuitOpen) || errors.Is(execErr, ErrHalfOpenLimitReached) {
			outcome.Attempts = append(outcome.Attempts, AttemptResult{Target: target, Skipped: true, Reason: rejectionReason(execErr)})
			continue
		}

		if execErr == nil {
			outcome.Attempts = append(outcome.Attempts, AttemptResult{Target: target})
			outcome.Served = target
			outcome.Succeeded = true
			outcome.FailoverUsed = i > 0
			return outcome, nil
		}

		// A real execution error: record it, and fail over unless the error is
		// classified as non-failoverable (e.g. caller fault).
		outcome.Attempts = append(outcome.Attempts, AttemptResult{Target: target, Err: execErr, Reason: execErr.Error()})
		lastErr = execErr
		if f.failoverable != nil && !f.failoverable(execErr) {
			return outcome, execErr
		}
		f.log.Debug("failover: provider failed, trying next candidate",
			logger.String("provider", target.Provider), logger.Err(execErr))
	}

	outcome.FailoverUsed = len(targets) > 1
	if lastErr != nil {
		return outcome, fmt.Errorf("%w: %w", ErrAllProvidersFailed, lastErr)
	}
	return outcome, fmt.Errorf("%w: all %d candidate(s) unavailable", ErrAllProvidersFailed, len(targets))
}

// rejectionReason renders a breaker rejection error as a short reason.
func rejectionReason(err error) string {
	if errors.Is(err, ErrHalfOpenLimitReached) {
		return "half-open limit reached"
	}
	return "circuit open"
}
