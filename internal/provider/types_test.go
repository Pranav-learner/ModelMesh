package provider

import (
	"errors"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestRole_Valid(t *testing.T) {
	valid := []Role{RoleSystem, RoleUser, RoleAssistant}
	for _, r := range valid {
		if !r.Valid() {
			t.Errorf("Role(%q).Valid() = false, want true", r)
		}
	}
	if Role("robot").Valid() {
		t.Errorf("Role(\"robot\").Valid() = true, want false")
	}
}

func TestChatRequest_Validate(t *testing.T) {
	valid := ChatRequest{
		Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}},
	}

	tests := []struct {
		name    string
		req     ChatRequest
		wantErr bool
	}{
		{"valid", valid, false},
		{"valid with sampling", ChatRequest{
			Messages:    valid.Messages,
			Temperature: ptr(0.7),
			TopP:        ptr(0.9),
			MaxTokens:   100,
		}, false},
		{"no messages", ChatRequest{}, true},
		{"bad role", ChatRequest{Messages: []ChatMessage{{Role: "x", Content: "hi"}}}, true},
		{"empty content", ChatRequest{Messages: []ChatMessage{{Role: RoleUser, Content: ""}}}, true},
		{"negative max tokens", ChatRequest{Messages: valid.Messages, MaxTokens: -1}, true},
		{"temperature too high", ChatRequest{Messages: valid.Messages, Temperature: ptr(2.5)}, true},
		{"temperature too low", ChatRequest{Messages: valid.Messages, Temperature: ptr(-0.1)}, true},
		{"top_p out of range", ChatRequest{Messages: valid.Messages, TopP: ptr(1.5)}, true},
		{"temperature zero is allowed", ChatRequest{Messages: valid.Messages, Temperature: ptr(0.0)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				if !errors.Is(err, ErrInvalidRequest) {
					t.Errorf("Validate() error does not wrap ErrInvalidRequest: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestEmbeddingRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     EmbeddingRequest
		wantErr bool
	}{
		{"valid", EmbeddingRequest{Input: []string{"a", "b"}}, false},
		{"no input", EmbeddingRequest{}, true},
		{"empty element", EmbeddingRequest{Input: []string{"a", ""}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("error does not wrap ErrInvalidRequest: %v", err)
			}
		})
	}
}

func TestModelInfo_Supports(t *testing.T) {
	m := ModelInfo{Capabilities: []Capability{CapabilityChat}}
	if !m.Supports(CapabilityChat) {
		t.Errorf("Supports(chat) = false, want true")
	}
	if m.Supports(CapabilityEmbeddings) {
		t.Errorf("Supports(embeddings) = true, want false")
	}
}

func TestHealthStatus_Healthy(t *testing.T) {
	if !(HealthStatus{State: HealthStateHealthy}).Healthy() {
		t.Errorf("healthy status reported not healthy")
	}
	if (HealthStatus{State: HealthStateDegraded}).Healthy() {
		t.Errorf("degraded status reported healthy")
	}
}
