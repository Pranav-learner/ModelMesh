package resilience

import (
	"context"
	"testing"
	"time"
)

func TestManager_PerProviderIndependence(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	ctx := context.Background()

	// Trip only the openai breaker.
	_ = m.Breaker("openai").Execute(ctx, fail)

	if m.State("openai") != StateOpen {
		t.Errorf("openai breaker = %s, want open", m.State("openai"))
	}
	if m.State("anthropic") != StateClosed {
		t.Errorf("anthropic breaker = %s, want closed (independent)", m.State("anthropic"))
	}
}

func TestManager_BreakerIsStable(t *testing.T) {
	m := NewManager(DefaultConfig())
	if m.Breaker("openai") != m.Breaker("openai") {
		t.Errorf("Breaker returned different instances for the same provider")
	}
}

func TestManager_States(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	ctx := context.Background()
	_ = m.Breaker("a").Execute(ctx, fail) // open
	_ = m.Breaker("b").Execute(ctx, ok)   // closed

	states := m.States()
	if states["a"] != StateOpen || states["b"] != StateClosed {
		t.Errorf("States() = %v", states)
	}
}

func TestManager_ResetAndResetAll(t *testing.T) {
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: time.Minute})
	ctx := context.Background()
	_ = m.Breaker("a").Execute(ctx, fail)
	_ = m.Breaker("b").Execute(ctx, fail)

	m.Reset("a")
	if m.State("a") != StateClosed {
		t.Errorf("Reset(a) did not close the breaker")
	}
	if m.State("b") != StateOpen {
		t.Errorf("Reset(a) affected breaker b")
	}

	m.ResetAll()
	if m.State("b") != StateClosed {
		t.Errorf("ResetAll did not close breaker b")
	}
}

func TestManager_SharedClock(t *testing.T) {
	clk := newClock()
	m := NewManager(Config{FailureThreshold: 1, OpenTimeout: 10 * time.Second}, WithManagerClock(clk.Now))
	ctx := context.Background()
	_ = m.Breaker("a").Execute(ctx, fail) // open

	clk.Advance(11 * time.Second)
	if m.State("a") != StateHalfOpen {
		t.Errorf("breaker did not use the injected manager clock: %s", m.State("a"))
	}
}

func TestManager_ConcurrentBreakerCreation(t *testing.T) {
	m := NewManager(DefaultConfig())
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() { _ = m.Breaker("shared"); done <- struct{}{} }()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	if len(m.States()) != 1 {
		t.Errorf("concurrent creation made %d breakers, want 1", len(m.States()))
	}
}
