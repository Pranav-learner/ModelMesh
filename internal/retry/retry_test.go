package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errRetryable = errors.New("retryable")
var errFatal = errors.New("fatal")

func retryable(err error) bool { return errors.Is(err, errRetryable) }

func fastPolicy(maxRetries int) Policy {
	return Policy{MaxRetries: maxRetries, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func TestDo_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		return nil
	}, retryable)

	if err != nil {
		t.Fatalf("Do() = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fastPolicy(3), func(context.Context) error {
		calls++
		if calls < 3 {
			return errRetryable
		}
		return nil
	}, retryable)

	if err != nil {
		t.Fatalf("Do() = %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (2 retries)", calls)
	}
}

func TestDo_ExhaustsRetries(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fastPolicy(2), func(context.Context) error {
		calls++
		return errRetryable
	}, retryable)

	if !errors.Is(err, errRetryable) {
		t.Fatalf("Do() = %v, want errRetryable", err)
	}
	if calls != 3 { // initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_DoesNotRetryNonRetryable(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fastPolicy(5), func(context.Context) error {
		calls++
		return errFatal
	}, retryable)

	if !errors.Is(err, errFatal) {
		t.Fatalf("Do() = %v, want errFatal", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on fatal)", calls)
	}
}

func TestDo_NilRetryablePredicateNeverRetries(t *testing.T) {
	calls := 0
	_ = Do(context.Background(), fastPolicy(5), func(context.Context) error {
		calls++
		return errRetryable
	}, nil)

	if calls != 1 {
		t.Errorf("calls = %d, want 1 when retryable is nil", calls)
	}
}

func TestDo_AlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx, fastPolicy(3), func(context.Context) error {
		calls++
		return nil
	}, retryable)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Errorf("calls = %d, want 0 (op must not run)", calls)
	}
}

func TestDo_ContextCancelledDuringBackoff(t *testing.T) {
	// Large backoff, short context: the first attempt fails retryably, then the
	// context expires during the wait and Do returns the operation error.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	calls := 0
	err := Do(ctx, Policy{MaxRetries: 5, BaseDelay: time.Second, MaxDelay: time.Second},
		func(context.Context) error {
			calls++
			return errRetryable
		}, retryable)

	if !errors.Is(err, errRetryable) {
		t.Fatalf("Do() = %v, want errRetryable (op error surfaced)", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (backoff interrupted)", calls)
	}
}

func TestBackoff_ExponentialAndCapped(t *testing.T) {
	p := Policy{BaseDelay: 10 * time.Millisecond, MaxDelay: 40 * time.Millisecond}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 10 * time.Millisecond},
		{1, 20 * time.Millisecond},
		{2, 40 * time.Millisecond},
		{3, 40 * time.Millisecond}, // capped
		{10, 40 * time.Millisecond},
	}
	for _, c := range cases {
		if got := backoff(p, c.attempt); got != c.want {
			t.Errorf("backoff(attempt=%d) = %s, want %s", c.attempt, got, c.want)
		}
	}
}

func TestPolicy_Normalized(t *testing.T) {
	p := Policy{}.normalized()
	if p.BaseDelay != DefaultBaseDelay || p.MaxDelay != DefaultMaxDelay {
		t.Errorf("normalized() did not apply delay defaults: %+v", p)
	}
	if (Policy{MaxRetries: -3}).normalized().MaxRetries != 0 {
		t.Errorf("negative MaxRetries should normalize to 0")
	}
}
