package shadow

import "errors"

// Sentinel errors for the shadow framework. Callers match with errors.Is.
var (
	// ErrInvalidConfig indicates a structurally invalid shadow configuration.
	ErrInvalidConfig = errors.New("invalid shadow config")

	// ErrUnknownPolicy indicates a policy name with no registered builder that is
	// not a known-reserved name.
	ErrUnknownPolicy = errors.New("unknown shadow policy")

	// ErrPolicyNotImplemented indicates a recognized, reserved policy that is not
	// yet implemented in this phase (e.g. rule-based sampling).
	ErrPolicyNotImplemented = errors.New("shadow policy not implemented")
)
