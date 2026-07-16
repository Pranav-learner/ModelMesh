package provider

import (
	"errors"
	"fmt"
	"testing"
)

func TestProviderError_UnwrapMatchesSentinel(t *testing.T) {
	err := NewError("openai", "chat", ErrTimeout)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("errors.Is(err, ErrTimeout) = false, want true")
	}
	if errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("errors.Is matched an unrelated sentinel")
	}
}

func TestProviderError_As(t *testing.T) {
	err := NewErrorf("anthropic", "embeddings", ErrNotImplemented, "no embeddings on %s", "anthropic")

	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(&ProviderError) = false, want true")
	}
	if pe.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", pe.Provider, "anthropic")
	}
	if pe.Op != "embeddings" {
		t.Errorf("Op = %q, want %q", pe.Op, "embeddings")
	}
	if !errors.Is(pe, ErrNotImplemented) {
		t.Errorf("wrapped sentinel not preserved")
	}
}

func TestProviderError_ErrorString(t *testing.T) {
	tests := []struct {
		name string
		err  *ProviderError
		want string
	}{
		{
			name: "provider op and wrapped",
			err:  &ProviderError{Provider: "openai", Op: "chat", Err: ErrTimeout},
			want: `provider "openai" op "chat": request timed out`,
		},
		{
			name: "provider op message and wrapped",
			err:  &ProviderError{Provider: "openai", Op: "chat", Err: ErrTimeout, Message: "deadline exceeded"},
			want: `provider "openai" op "chat": deadline exceeded: request timed out`,
		},
		{
			name: "only op",
			err:  &ProviderError{Op: "register", Err: ErrProviderExists},
			want: `op "register": provider already registered`,
		},
		{
			name: "empty",
			err:  &ProviderError{},
			want: "provider error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderError_WrappingChain(t *testing.T) {
	// A ProviderError wrapping an fmt-wrapped sentinel must still match.
	underlying := fmt.Errorf("%w: bad field", ErrInvalidRequest)
	err := NewError("mock", "chat", underlying)

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("errors.Is did not traverse the full wrap chain")
	}
}
