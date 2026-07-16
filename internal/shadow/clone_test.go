package shadow

import (
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestCloneRequest_IsolatedFromOriginal(t *testing.T) {
	temp := 0.5
	topP := 0.9
	req := provider.ChatRequest{
		Model:       "gpt-4",
		Messages:    []provider.ChatMessage{{Role: provider.RoleUser, Content: "original"}},
		Stop:        []string{"STOP"},
		Metadata:    map[string]string{"user": "alice"},
		Temperature: &temp,
		TopP:        &topP,
	}

	clone := cloneRequest(req)

	// Mutate every mutable field of the original.
	req.Messages[0].Content = "mutated"
	req.Stop[0] = "MUTATED"
	req.Metadata["user"] = "mallory"
	*req.Temperature = 9.9
	*req.TopP = 9.9

	if clone.Messages[0].Content != "original" {
		t.Errorf("clone messages leaked mutation: %q", clone.Messages[0].Content)
	}
	if clone.Stop[0] != "STOP" {
		t.Errorf("clone stop leaked mutation: %q", clone.Stop[0])
	}
	if clone.Metadata["user"] != "alice" {
		t.Errorf("clone metadata leaked mutation: %q", clone.Metadata["user"])
	}
	if *clone.Temperature != 0.5 || *clone.TopP != 0.9 {
		t.Errorf("clone pointer fields leaked mutation: temp=%v topP=%v", *clone.Temperature, *clone.TopP)
	}
	// And the reverse: mutating the clone must not touch the original.
	clone.Metadata["user"] = "bob"
	if req.Metadata["user"] != "mallory" {
		t.Errorf("original metadata mutated by clone change")
	}
}

func TestCloneRequest_NilFields(t *testing.T) {
	clone := cloneRequest(provider.ChatRequest{Model: "m"})
	if clone.Messages != nil || clone.Stop != nil || clone.Metadata != nil || clone.Temperature != nil {
		t.Errorf("nil fields should stay nil after clone: %+v", clone)
	}
}
