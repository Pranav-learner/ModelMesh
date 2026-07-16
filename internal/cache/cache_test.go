package cache

import (
	"testing"
	"time"
)

func TestEntry_Expired(t *testing.T) {
	now := time.Unix(1000, 0)
	if (Entry{ExpiresAt: now.Add(time.Second)}).Expired(now) {
		t.Errorf("future expiry reported expired")
	}
	if !(Entry{ExpiresAt: now.Add(-time.Second)}).Expired(now) {
		t.Errorf("past expiry reported not expired")
	}
	if (Entry{}).Expired(now) {
		t.Errorf("zero ExpiresAt (never) reported expired")
	}
}

func TestEntry_RemainingTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	if d := (Entry{ExpiresAt: now.Add(10 * time.Second)}).RemainingTTL(now); d != 10*time.Second {
		t.Errorf("RemainingTTL = %v, want 10s", d)
	}
	if d := (Entry{ExpiresAt: now.Add(-time.Second)}).RemainingTTL(now); d != 0 {
		t.Errorf("expired RemainingTTL = %v, want 0", d)
	}
	if d := (Entry{}).RemainingTTL(now); d != 0 {
		t.Errorf("never-expiry RemainingTTL = %v, want 0", d)
	}
}

func TestEntry_Age(t *testing.T) {
	now := time.Unix(1000, 0)
	if d := (Entry{CreatedAt: now.Add(-30 * time.Second)}).Age(now); d != 30*time.Second {
		t.Errorf("Age = %v, want 30s", d)
	}
}
