package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/symbiotes/modelmesh/internal/retry"
)

func targets(names ...string) []Target {
	out := make([]Target, len(names))
	for i, n := range names {
		out[i] = Target{Provider: n, Model: n + "-model"}
	}
	return out
}

func TestFailover_FirstCandidateSucceeds(t *testing.T) {
	f := NewFailover(NewManager(DefaultConfig()))
	var served string
	out, err := f.Do(context.Background(), targets("a", "b"), func(_ context.Context, tg Target) error {
		served = tg.Provider
		return nil
	})
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if served != "a" || out.Served.Provider != "a" || out.FailoverUsed {
		t.Errorf("outcome = %+v, served=%q", out, served)
	}
}

func TestFailover_FailsOverToNext(t *testing.T) {
	f := NewFailover(NewManager(Config{FailureThreshold: 5, OpenTimeout: time.Minute}))
	out, err := f.Do(context.Background(), targets("a", "b"), func(_ context.Context, tg Target) error {
		if tg.Provider == "a" {
			return errBoom // a fails
		}
		return nil // b succeeds
	})
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if out.Served.Provider != "b" || !out.FailoverUsed {
		t.Errorf("outcome = %+v, want served b with failover", out)
	}
	if len(out.Attempts) != 2 || out.Attempts[0].Err == nil || out.Attempts[1].Err != nil {
		t.Errorf("attempts = %+v", out.Attempts)
	}
}

func TestFailover_SkipsOpenCircuit(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	m.Breaker("a").RecordFailure() // trip a's breaker open
	f := NewFailover(m)

	out, err := f.Do(context.Background(), targets("a", "b"), func(_ context.Context, tg Target) error {
		if tg.Provider == "a" {
			t.Errorf("provider a should have been skipped (open circuit)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	if out.Served.Provider != "b" || !out.Attempts[0].Skipped || out.Attempts[0].Reason != "circuit open" {
		t.Errorf("outcome = %+v", out)
	}
}

func TestFailover_AllFail(t *testing.T) {
	f := NewFailover(NewManager(Config{FailureThreshold: 5, OpenTimeout: time.Minute}))
	_, err := f.Do(context.Background(), targets("a", "b"), func(context.Context, Target) error {
		return errBoom
	})
	if !errors.Is(err, ErrAllProvidersFailed) {
		t.Fatalf("Do() = %v, want ErrAllProvidersFailed", err)
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("error should wrap the last failure")
	}
}

func TestFailover_AllSkipped(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	m.Breaker("a").RecordFailure()
	m.Breaker("b").RecordFailure()
	f := NewFailover(m)

	_, err := f.Do(context.Background(), targets("a", "b"), func(context.Context, Target) error {
		t.Errorf("no provider should be called when all circuits are open")
		return nil
	})
	if !errors.Is(err, ErrAllProvidersFailed) {
		t.Errorf("Do() = %v, want ErrAllProvidersFailed", err)
	}
}

func TestFailover_NonFailoverableReturnsImmediately(t *testing.T) {
	callerFault := errors.New("bad request")
	f := NewFailover(NewManager(DefaultConfig()),
		WithFailoverable(func(err error) bool { return !errors.Is(err, callerFault) }))

	calls := 0
	_, err := f.Do(context.Background(), targets("a", "b"), func(_ context.Context, tg Target) error {
		calls++
		return callerFault
	})
	if !errors.Is(err, callerFault) {
		t.Fatalf("Do() = %v, want the caller-fault error", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no failover on caller fault)", calls)
	}
}

func TestFailover_RetryWithinProvider(t *testing.T) {
	// A provider that fails twice then succeeds, with retry cooperating.
	f := NewFailover(NewManager(Config{FailureThreshold: 5, OpenTimeout: time.Minute}),
		WithRetryPolicy(retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}),
		WithRetryable(func(error) bool { return true }))

	attempts := 0
	out, err := f.Do(context.Background(), targets("a"), func(context.Context, Target) error {
		attempts++
		if attempts < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do() = %v, want success after retries", err)
	}
	if attempts != 3 || !out.Succeeded || out.FailoverUsed {
		t.Errorf("attempts=%d outcome=%+v", attempts, out)
	}
}

func TestFailover_ExplainDiagnostics(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	m.Breaker("a").RecordFailure()
	f := NewFailover(m)
	out, _ := f.Do(context.Background(), targets("a", "b"), func(context.Context, Target) error { return nil })

	text := ExplainFailover(out)
	if !contains(text, "SKIPPED") || !contains(text, "served by b") {
		t.Errorf("ExplainFailover = %q", text)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
