// Package adaptererr centralizes translation of upstream provider failures into
// ModelMesh's normalized error model. Both the OpenAI and Anthropic adapters
// reduce their SDK-specific errors to an HTTP status code (plus a short message)
// and hand them here, so the status -> sentinel mapping lives in exactly one
// place and stays consistent across providers.
//
// Nothing outside an adapter should ever observe a raw SDK error; adapters call
// FromStatus / FromContext and return the resulting *provider.ProviderError.
package adaptererr

import (
	"context"
	"errors"
	"net/http"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// FromStatus maps an upstream HTTP status code to a wrapped ModelMesh error,
// annotated with the provider name and operation. The mapping is:
//
//	401, 403           -> ErrAuthenticationFailed
//	404                -> ErrModelNotFound
//	408                -> ErrTimeout
//	429                -> ErrRateLimited
//	5xx                -> ErrProviderUnavailable
//	other 4xx          -> ErrInvalidRequest
//	anything else      -> ErrProviderUnavailable
func FromStatus(providerName, op string, status int, message string) error {
	sentinel := sentinelForStatus(status)
	if message == "" {
		return provider.NewError(providerName, op, sentinel)
	}
	return provider.NewErrorf(providerName, op, sentinel, "upstream status %d: %s", status, message)
}

func sentinelForStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return provider.ErrAuthenticationFailed
	case status == http.StatusNotFound:
		return provider.ErrModelNotFound
	case status == http.StatusRequestTimeout:
		return provider.ErrTimeout
	case status == http.StatusTooManyRequests:
		return provider.ErrRateLimited
	case status >= 500:
		return provider.ErrProviderUnavailable
	case status >= 400:
		return provider.ErrInvalidRequest
	default:
		return provider.ErrProviderUnavailable
	}
}

// FromContext maps a context error (cancellation/deadline) to ErrTimeout,
// wrapped with provider/op context. It returns nil if err is not a context
// error, letting callers fall through to other classification.
func FromContext(providerName, op string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return provider.NewError(providerName, op, provider.ErrTimeout)
	}
	return nil
}

// Unexpected wraps an otherwise-unclassified error as ErrProviderUnavailable.
// It is the safe default so callers never receive a raw SDK error.
func Unexpected(providerName, op string, err error) error {
	if err == nil {
		return nil
	}
	return provider.NewErrorf(providerName, op, provider.ErrProviderUnavailable, "%v", err)
}

// Retryable reports whether err represents a transient failure worth retrying.
// It is the predicate the adapters pass to retry.Do: rate limiting, upstream
// unavailability, and timeouts are transient; auth, not-found, and invalid
// request are not.
func Retryable(err error) bool {
	switch {
	case errors.Is(err, provider.ErrRateLimited),
		errors.Is(err, provider.ErrProviderUnavailable),
		errors.Is(err, provider.ErrTimeout):
		return true
	default:
		return false
	}
}
