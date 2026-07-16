// Package retry provides a minimal, dependency-free retry helper with
// exponential backoff that respects context cancellation.
//
// It is intentionally small and unopinionated: the caller supplies the operation
// and a predicate deciding which errors are worth retrying. This keeps retry
// policy (how many times, how long to wait) separate from retry classification
// (which errors are transient), and separate again from the operation itself.
//
// This is NOT a circuit breaker. There is no shared failure state, no tripping,
// and no cross-request coordination — that is Phase 4. This helper only retries
// a single logical call a bounded number of times.
package retry

import (
	"context"
	"time"
)

// Default policy parameters used when a Policy leaves them unset.
const (
	DefaultMaxRetries = 2
	DefaultBaseDelay  = 100 * time.Millisecond
	DefaultMaxDelay   = 2 * time.Second
)

// Policy configures retry behavior.
type Policy struct {
	// MaxRetries is the number of retries after the initial attempt. Zero means
	// the operation is attempted exactly once.
	MaxRetries int
	// BaseDelay is the backoff before the first retry; it doubles each attempt.
	BaseDelay time.Duration
	// MaxDelay caps the per-attempt backoff.
	MaxDelay time.Duration
}

// DefaultPolicy returns a sensible default retry policy.
func DefaultPolicy() Policy {
	return Policy{
		MaxRetries: DefaultMaxRetries,
		BaseDelay:  DefaultBaseDelay,
		MaxDelay:   DefaultMaxDelay,
	}
}

// normalized fills zero-valued delay fields with defaults. MaxRetries is left as
// given (zero is a valid "no retries" value).
func (p Policy) normalized() Policy {
	if p.BaseDelay <= 0 {
		p.BaseDelay = DefaultBaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultMaxDelay
	}
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	return p
}

// Do executes op, retrying up to Policy.MaxRetries times while retryable(err)
// reports true, with exponential backoff between attempts.
//
// Semantics:
//   - op is always attempted at least once.
//   - If op returns nil, Do returns nil immediately.
//   - If op returns an error for which retryable is nil or returns false, Do
//     returns that error without retrying.
//   - Between attempts Do waits with exponential backoff, but a cancelled
//     context aborts the wait and Do returns the most recent error.
//   - If the context is already cancelled before an attempt, Do returns the
//     context error (or the last operation error if one exists).
func Do(ctx context.Context, p Policy, op func(context.Context) error, retryable func(error) bool) error {
	p = p.normalized()

	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}

		lastErr = op(ctx)
		if lastErr == nil {
			return nil
		}

		// Stop if we are out of retries or the error is not retryable.
		if attempt >= p.MaxRetries || retryable == nil || !retryable(lastErr) {
			return lastErr
		}

		if err := sleep(ctx, backoff(p, attempt)); err != nil {
			// Context ended during backoff; surface the operation error, which is
			// more actionable than the context error for the caller.
			return lastErr
		}
	}
}

// backoff computes the delay before the retry following the given (zero-based)
// attempt: BaseDelay * 2^attempt, capped at MaxDelay.
func backoff(p Policy, attempt int) time.Duration {
	d := p.BaseDelay
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	if d > p.MaxDelay {
		return p.MaxDelay
	}
	return d
}

// sleep waits for d or until ctx is done, returning ctx.Err() if it ended first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
