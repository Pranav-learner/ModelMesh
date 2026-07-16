package budget

import (
	"testing"
	"time"
)

func TestBudgetConstructors(t *testing.T) {
	u := UserBudget("u-1", 5)
	if u.Scope != ScopeUser || u.ID != "u-1" || u.DailyLimit != 5 {
		t.Errorf("UserBudget = %+v", u)
	}
	tm := TeamBudget("t-1", 50)
	if tm.Scope != ScopeTeam || !tm.valid() {
		t.Errorf("TeamBudget = %+v", tm)
	}
	if (Budget{}).valid() {
		t.Errorf("empty budget should be invalid")
	}
	if (Budget{ID: "x", Scope: ScopeUser, DailyLimit: -1}).valid() {
		t.Errorf("negative daily limit should be invalid")
	}
}

func TestManagedBudget_Rollover(t *testing.T) {
	start := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	mb := &managedBudget{desc: UserBudget("u", 10), usage: 7, resetAt: nextReset(start)}

	// Before reset: usage preserved.
	mb.rollover(start.Add(time.Hour))
	if mb.usage != 7 {
		t.Errorf("usage before reset = %v, want 7", mb.usage)
	}

	// After the window boundary: usage resets and resetAt advances past now.
	after := start.Add(30 * time.Hour)
	mb.rollover(after)
	if mb.usage != 0 {
		t.Errorf("usage after rollover = %v, want 0", mb.usage)
	}
	if !mb.resetAt.After(after) {
		t.Errorf("resetAt %v should be after now %v", mb.resetAt, after)
	}
}

func TestManagedBudget_RemainingFloored(t *testing.T) {
	mb := &managedBudget{desc: UserBudget("u", 10), usage: 15}
	if got := mb.remaining(); got != 0 {
		t.Errorf("remaining over-limit = %v, want 0 (floored)", got)
	}
}
