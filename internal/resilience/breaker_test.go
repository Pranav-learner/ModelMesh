package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock for deterministic transition tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1_000_000, 0)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var errBoom = errors.New("boom")

func fail(context.Context) error { return errBoom }
func ok(context.Context) error   { return nil }

func TestBreaker_StartsClosed(t *testing.T) {
	if b := NewBreaker(DefaultConfig()); b.State() != StateClosed {
		t.Errorf("new breaker state = %s, want closed", b.State())
	}
}

func TestBreaker_ClosedToOpenOnThreshold(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 3, OpenTimeout: time.Minute})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_ = b.Execute(ctx, fail)
		if b.State() != StateClosed {
			t.Fatalf("after %d failures state = %s, want closed", i+1, b.State())
		}
	}
	_ = b.Execute(ctx, fail) // 3rd failure trips
	if b.State() != StateOpen {
		t.Errorf("after threshold state = %s, want open", b.State())
	}
}

func TestBreaker_SuccessResetsFailureStreak(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 3, OpenTimeout: time.Minute})
	ctx := context.Background()

	_ = b.Execute(ctx, fail)
	_ = b.Execute(ctx, fail)
	_ = b.Execute(ctx, ok) // resets streak
	_ = b.Execute(ctx, fail)
	_ = b.Execute(ctx, fail)
	if b.State() != StateClosed {
		t.Errorf("state = %s, want closed (streak reset by success)", b.State())
	}
}

func TestBreaker_OpenRejectsWithoutCallingFn(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	ctx := context.Background()
	_ = b.Execute(ctx, fail) // trips open

	called := false
	err := b.Execute(ctx, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Execute(open) = %v, want ErrCircuitOpen", err)
	}
	if !errors.Is(err, ErrTooManyFailures) {
		t.Errorf("open rejection should also match ErrTooManyFailures")
	}
	if called {
		t.Errorf("fn was called while circuit open")
	}
}

func TestBreaker_OpenToHalfOpenAfterCooldown(t *testing.T) {
	clk := newClock()
	b := NewBreaker(Config{FailureThreshold: 1, OpenTimeout: 30 * time.Second}, WithClock(clk.Now))
	_ = b.Execute(context.Background(), fail) // open

	clk.Advance(29 * time.Second)
	if b.State() != StateOpen {
		t.Errorf("before cooldown state = %s, want open", b.State())
	}
	clk.Advance(2 * time.Second) // total 31s > 30s
	if b.State() != StateHalfOpen {
		t.Errorf("after cooldown state = %s, want half_open", b.State())
	}
}

func TestBreaker_HalfOpenToClosedOnSuccesses(t *testing.T) {
	clk := newClock()
	b := NewBreaker(Config{FailureThreshold: 1, SuccessThreshold: 2, OpenTimeout: time.Second, HalfOpenMaxRequests: 5}, WithClock(clk.Now))
	ctx := context.Background()
	_ = b.Execute(ctx, fail) // open
	clk.Advance(2 * time.Second)

	_ = b.Execute(ctx, ok) // 1st probe success
	if b.State() != StateHalfOpen {
		t.Fatalf("after 1 success state = %s, want half_open", b.State())
	}
	_ = b.Execute(ctx, ok) // 2nd probe success -> close
	if b.State() != StateClosed {
		t.Errorf("after success threshold state = %s, want closed", b.State())
	}
}

func TestBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	clk := newClock()
	b := NewBreaker(Config{FailureThreshold: 1, SuccessThreshold: 3, OpenTimeout: time.Second, HalfOpenMaxRequests: 5}, WithClock(clk.Now))
	ctx := context.Background()
	_ = b.Execute(ctx, fail) // open
	clk.Advance(2 * time.Second)

	_ = b.Execute(ctx, ok)   // half-open, one success
	_ = b.Execute(ctx, fail) // a probe fails -> reopen
	if b.State() != StateOpen {
		t.Errorf("after half-open failure state = %s, want open", b.State())
	}
}

func TestBreaker_HalfOpenRequestLimit(t *testing.T) {
	clk := newClock()
	b := NewBreaker(Config{FailureThreshold: 1, SuccessThreshold: 5, OpenTimeout: time.Second, HalfOpenMaxRequests: 1}, WithClock(clk.Now))
	ctx := context.Background()
	_ = b.Execute(ctx, fail) // open
	clk.Advance(2 * time.Second)

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = b.Execute(ctx, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started // the single probe slot is now taken

	err := b.Execute(ctx, ok)
	if !errors.Is(err, ErrHalfOpenLimitReached) {
		t.Errorf("second half-open request = %v, want ErrHalfOpenLimitReached", err)
	}
	close(release)
}

func TestBreaker_Reset(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	_ = b.Execute(context.Background(), fail) // open
	if b.State() != StateOpen {
		t.Fatalf("precondition: state = %s, want open", b.State())
	}
	b.Reset()
	if b.State() != StateClosed {
		t.Errorf("after Reset state = %s, want closed", b.State())
	}
}

func TestBreaker_ExecuteReturnsFnError(t *testing.T) {
	b := NewBreaker(DefaultConfig())
	if err := b.Execute(context.Background(), fail); !errors.Is(err, errBoom) {
		t.Errorf("Execute returned %v, want the fn's error", err)
	}
}

func TestBreaker_IsFailureClassifier(t *testing.T) {
	// Only errBoom counts as a failure; other errors are treated as success.
	notAFailure := errors.New("caller fault")
	b := NewBreaker(Config{
		FailureThreshold: 2,
		OpenTimeout:      time.Minute,
		IsFailure:        func(err error) bool { return errors.Is(err, errBoom) },
	})
	ctx := context.Background()

	// Two non-failure errors do not trip the breaker.
	_ = b.Execute(ctx, func(context.Context) error { return notAFailure })
	_ = b.Execute(ctx, func(context.Context) error { return notAFailure })
	if b.State() != StateClosed {
		t.Errorf("non-failure errors tripped the breaker: %s", b.State())
	}
	// Two real failures do.
	_ = b.Execute(ctx, fail)
	_ = b.Execute(ctx, fail)
	if b.State() != StateOpen {
		t.Errorf("real failures did not trip the breaker: %s", b.State())
	}
}

func TestBreaker_RecordSuccessFailure(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 2, OpenTimeout: time.Minute})
	b.RecordFailure()
	b.RecordSuccess() // resets streak
	b.RecordFailure()
	if b.State() != StateClosed {
		t.Errorf("state = %s, want closed", b.State())
	}
	b.RecordFailure() // 2 consecutive now -> open
	if b.State() != StateOpen {
		t.Errorf("state = %s, want open", b.State())
	}
}

func TestBreaker_Counts(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 5, OpenTimeout: time.Minute})
	b.RecordFailure()
	b.RecordFailure()
	c := b.Counts()
	if c.State != StateClosed || c.ConsecutiveFailures != 2 {
		t.Errorf("Counts = %+v, want closed/2", c)
	}
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	b := NewBreaker(Config{FailureThreshold: 10, SuccessThreshold: 3, OpenTimeout: time.Millisecond, HalfOpenMaxRequests: 4})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = b.Execute(ctx, ok)
			} else {
				_ = b.Execute(ctx, fail)
			}
			_ = b.State()
			_ = b.Counts()
			if i%20 == 0 {
				b.Reset()
			}
		}(i)
	}
	wg.Wait()
	// No assertion beyond "no race / no panic"; the state must be a valid value.
	if s := b.State(); s != StateClosed && s != StateOpen && s != StateHalfOpen {
		t.Errorf("invalid final state: %v", s)
	}
}
