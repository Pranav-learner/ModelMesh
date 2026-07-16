package cache

import (
	"strings"
	"testing"
	"time"
)

func TestExplainHit(t *testing.T) {
	cases := []struct {
		entry Entry
		found bool
		want  string
	}{
		{Entry{}, false, "miss"},
		{Entry{Level: LevelL1}, true, "L1 memory"},
		{Entry{Level: LevelL2}, true, "L2 Redis"},
		{Entry{Level: LevelL3, Similarity: 0.9412}, true, "0.9412"},
	}
	for _, c := range cases {
		got := ExplainHit(c.entry, c.found)
		if !strings.Contains(got, c.want) {
			t.Errorf("ExplainHit(%+v) = %q, want to contain %q", c.entry, got, c.want)
		}
	}
}

func TestInspectEntry(t *testing.T) {
	now := time.Unix(1000, 0)
	e := Entry{
		Key:        "k",
		Value:      []byte("hello"),
		Level:      LevelL3,
		Similarity: 0.95,
		CreatedAt:  now.Add(-10 * time.Second),
		ExpiresAt:  now.Add(50 * time.Second),
	}
	got := InspectEntry(e, now)
	for _, want := range []string{"L3 (semantic)", "bytes=5", "similarity=0.9500"} {
		if !strings.Contains(got, want) {
			t.Errorf("InspectEntry = %q, want to contain %q", got, want)
		}
	}
}

func TestLayerUsed(t *testing.T) {
	if LayerUsed(Entry{Level: LevelL1}) != "L1 (memory)" {
		t.Errorf("LayerUsed(l1) wrong")
	}
	if LayerUsed(Entry{}) != "none" {
		t.Errorf("LayerUsed(empty) should be none")
	}
}
