package provider

import (
	"errors"
	"fmt"
)

// This file centralizes the Provider Layer's error model. It follows idiomatic
// Go conventions:
//
//   - Sentinel errors are comparable with errors.Is and act as stable,
//     matchable categories.
//   - ProviderError is a structured, wrapping error that attaches provider and
//     operation context while remaining matchable (via Unwrap) against the
//     sentinels above.
//
// Callers should match on the sentinels (errors.Is) rather than on string
// contents, and may extract structured detail with errors.As(&ProviderError{}).

// Sentinel errors. These are the stable categories the rest of the system, and
// future phases (routing, circuit breaking), will branch on.
var (
	// ErrProviderNotFound indicates a lookup for an unregistered provider.
	ErrProviderNotFound = errors.New("provider not found")

	// ErrProviderExists indicates an attempt to register a provider name that is
	// already registered.
	ErrProviderExists = errors.New("provider already registered")

	// ErrProviderUnavailable indicates a provider is registered but cannot
	// currently serve requests (e.g. failed health check, disabled, or the
	// upstream returned a 5xx).
	ErrProviderUnavailable = errors.New("provider unavailable")

	// ErrUnsupportedModel indicates the requested model is not offered by the
	// selected provider (determined locally, before dispatch).
	ErrUnsupportedModel = errors.New("unsupported model")

	// ErrAuthenticationFailed indicates the provider rejected our credentials
	// (typically an upstream 401/403).
	ErrAuthenticationFailed = errors.New("authentication failed")

	// ErrModelNotFound indicates the upstream reported the requested model does
	// not exist (typically an upstream 404). It is distinct from
	// ErrUnsupportedModel, which is a local, pre-dispatch determination.
	ErrModelNotFound = errors.New("model not found")

	// ErrRateLimited indicates the provider throttled the request (upstream 429).
	ErrRateLimited = errors.New("rate limited")

	// ErrInvalidRequest indicates a structurally invalid request. DTO Validate
	// methods wrap this error.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrTimeout indicates an operation exceeded its deadline. Adapters should
	// map context deadline/cancellation and provider timeouts onto this.
	ErrTimeout = errors.New("request timed out")

	// ErrNotImplemented indicates a capability the provider does not implement
	// (e.g. an embeddings call to a chat-only provider).
	ErrNotImplemented = errors.New("not implemented")
)

// ProviderError enriches an underlying error with the provider name and the
// operation being performed. It is the recommended wrapper for adapters to
// return, giving callers structured context while preserving errors.Is matching
// against the wrapped sentinel.
//
// Example:
//
//	return provider.NewError("openai", "chat", provider.ErrTimeout)
//	// errors.Is(err, provider.ErrTimeout) == true
//	// var pe *provider.ProviderError; errors.As(err, &pe) == true
type ProviderError struct {
	// Provider is the provider name the error originated from. May be empty for
	// errors raised before a provider is resolved (e.g. registry lookups).
	Provider string
	// Op is the logical operation, e.g. "chat", "embeddings", "health_check",
	// "register", "lookup".
	Op string
	// Err is the wrapped error, typically one of the sentinels above or an
	// underlying transport error.
	Err error
	// Message is an optional human-readable detail. It must never contain
	// secrets or raw provider payloads.
	Message string
}

// Error implements the error interface with a stable, greppable format.
func (e *ProviderError) Error() string {
	var prefix string
	switch {
	case e.Provider != "" && e.Op != "":
		prefix = fmt.Sprintf("provider %q op %q", e.Provider, e.Op)
	case e.Provider != "":
		prefix = fmt.Sprintf("provider %q", e.Provider)
	case e.Op != "":
		prefix = fmt.Sprintf("op %q", e.Op)
	default:
		prefix = "provider error"
	}

	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", prefix, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", prefix, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", prefix, e.Err)
	default:
		return prefix
	}
}

// Unwrap enables errors.Is / errors.As to see through to the wrapped error.
func (e *ProviderError) Unwrap() error { return e.Err }

// NewError constructs a ProviderError wrapping err with provider and op context.
func NewError(providerName, op string, err error) *ProviderError {
	return &ProviderError{Provider: providerName, Op: op, Err: err}
}

// NewErrorf constructs a ProviderError with a formatted human-readable message
// in addition to the wrapped error.
func NewErrorf(providerName, op string, err error, format string, args ...any) *ProviderError {
	return &ProviderError{
		Provider: providerName,
		Op:       op,
		Err:      err,
		Message:  fmt.Sprintf(format, args...),
	}
}
