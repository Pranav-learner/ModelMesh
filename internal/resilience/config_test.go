package resilience

import (
	"errors"
	"testing"
	"time"
)

func TestConfig_DefaultsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("DefaultConfig().Validate() = %v", err)
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	c := Config{}.WithDefaults()
	if c.FailureThreshold != DefaultFailureThreshold ||
		c.SuccessThreshold != DefaultSuccessThreshold ||
		c.OpenTimeout != DefaultOpenTimeout ||
		c.HalfOpenMaxRequests != DefaultHalfOpenMaxRequests {
		t.Errorf("WithDefaults did not fill defaults: %+v", c)
	}
	// A supplied value is preserved.
	if got := (Config{FailureThreshold: 9}).WithDefaults().FailureThreshold; got != 9 {
		t.Errorf("WithDefaults overwrote a supplied value: %d", got)
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []Config{
		{FailureThreshold: 0, SuccessThreshold: 1, OpenTimeout: time.Second, HalfOpenMaxRequests: 1},
		{FailureThreshold: 1, SuccessThreshold: 0, OpenTimeout: time.Second, HalfOpenMaxRequests: 1},
		{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: 0, HalfOpenMaxRequests: 1},
		{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Second, HalfOpenMaxRequests: 0},
	}
	for i, c := range cases {
		if err := c.Validate(); !errors.Is(err, ErrInvalidBreakerConfig) {
			t.Errorf("case %d: Validate() = %v, want ErrInvalidBreakerConfig", i, err)
		}
	}
	valid := Config{FailureThreshold: 1, SuccessThreshold: 1, OpenTimeout: time.Second, HalfOpenMaxRequests: 1}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestState_String(t *testing.T) {
	cases := map[State]string{StateClosed: "closed", StateOpen: "open", StateHalfOpen: "half_open", State(99): "unknown"}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("State(%d).String() = %q, want %q", s, s.String(), want)
		}
	}
}
