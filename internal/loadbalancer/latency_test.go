package loadbalancer

import (
	"testing"
	"time"
)

func TestRollingLatency_AverageAndWindow(t *testing.T) {
	r := newRollingLatency(3)
	if r.samples() != 0 || r.average() != 0 {
		t.Fatalf("empty tracker: samples=%d average=%v, want 0/0", r.samples(), r.average())
	}

	r.record(10 * time.Millisecond)
	r.record(20 * time.Millisecond)
	if got, want := r.average(), 15*time.Millisecond; got != want {
		t.Errorf("average after 2 samples = %v, want %v", got, want)
	}
	if r.samples() != 2 {
		t.Errorf("samples = %d, want 2", r.samples())
	}

	// Fill and overflow the window: only the last 3 samples count.
	r.record(30 * time.Millisecond) // window: 10,20,30 -> avg 20
	if got, want := r.average(), 20*time.Millisecond; got != want {
		t.Errorf("average full window = %v, want %v", got, want)
	}
	r.record(60 * time.Millisecond) // evicts 10 -> window 20,30,60 -> avg ~36.6
	if got, want := r.average(), (20+30+60)*time.Millisecond/3; got != want {
		t.Errorf("average after eviction = %v, want %v", got, want)
	}
	if r.samples() != 3 {
		t.Errorf("samples capped = %d, want 3", r.samples())
	}
}

func TestRollingLatency_NonPositiveWindowDefaults(t *testing.T) {
	r := newRollingLatency(0)
	if len(r.window) != DefaultLatencyWindow {
		t.Errorf("window size = %d, want default %d", len(r.window), DefaultLatencyWindow)
	}
}
