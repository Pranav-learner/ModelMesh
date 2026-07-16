package adaptererr

import (
	"context"
	"errors"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func TestFromStatus_Mapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, provider.ErrAuthenticationFailed},
		{403, provider.ErrAuthenticationFailed},
		{404, provider.ErrModelNotFound},
		{408, provider.ErrTimeout},
		{429, provider.ErrRateLimited},
		{500, provider.ErrProviderUnavailable},
		{503, provider.ErrProviderUnavailable},
		{400, provider.ErrInvalidRequest},
		{422, provider.ErrInvalidRequest},
		{200, provider.ErrProviderUnavailable}, // unexpected non-error status
	}
	for _, c := range cases {
		err := FromStatus("openai", "chat", c.status, "boom")
		if !errors.Is(err, c.want) {
			t.Errorf("FromStatus(%d) = %v, want wrap of %v", c.status, err, c.want)
		}
		var pe *provider.ProviderError
		if !errors.As(err, &pe) || pe.Provider != "openai" || pe.Op != "chat" {
			t.Errorf("FromStatus(%d) not wrapped with provider/op context: %v", c.status, err)
		}
	}
}

func TestFromStatus_NoMessage(t *testing.T) {
	err := FromStatus("openai", "chat", 429, "")
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func TestFromContext(t *testing.T) {
	if err := FromContext("p", "op", context.Canceled); !errors.Is(err, provider.ErrTimeout) {
		t.Errorf("context.Canceled -> %v, want ErrTimeout", err)
	}
	if err := FromContext("p", "op", context.DeadlineExceeded); !errors.Is(err, provider.ErrTimeout) {
		t.Errorf("context.DeadlineExceeded -> %v, want ErrTimeout", err)
	}
	if err := FromContext("p", "op", errors.New("other")); err != nil {
		t.Errorf("non-context error -> %v, want nil", err)
	}
}

func TestUnexpected(t *testing.T) {
	if err := Unexpected("p", "op", nil); err != nil {
		t.Errorf("Unexpected(nil) = %v, want nil", err)
	}
	err := Unexpected("p", "op", errors.New("weird"))
	if !errors.Is(err, provider.ErrProviderUnavailable) {
		t.Errorf("Unexpected() = %v, want wrap of ErrProviderUnavailable", err)
	}
}

func TestRetryable(t *testing.T) {
	retryables := []error{provider.ErrRateLimited, provider.ErrProviderUnavailable, provider.ErrTimeout}
	for _, e := range retryables {
		if !Retryable(provider.NewError("p", "op", e)) {
			t.Errorf("Retryable(%v) = false, want true", e)
		}
	}
	nonRetryables := []error{
		provider.ErrAuthenticationFailed,
		provider.ErrModelNotFound,
		provider.ErrInvalidRequest,
		provider.ErrNotImplemented,
	}
	for _, e := range nonRetryables {
		if Retryable(provider.NewError("p", "op", e)) {
			t.Errorf("Retryable(%v) = true, want false", e)
		}
	}
}
