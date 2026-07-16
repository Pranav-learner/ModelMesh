package budget

import "errors"

// Sentinel errors for the budget engine. Callers match with errors.Is.
var (
	// ErrInvalidConfig indicates a structurally invalid budget configuration.
	ErrInvalidConfig = errors.New("invalid budget config")

	// ErrInvalidBudget indicates a budget missing a required field (ID or scope)
	// or carrying a negative daily limit.
	ErrInvalidBudget = errors.New("invalid budget")

	// ErrInvalidScope indicates a scope that is neither user nor team.
	ErrInvalidScope = errors.New("invalid budget scope")

	// ErrUnknownPolicy indicates a policy name with no registered builder that is
	// not a known-reserved name.
	ErrUnknownPolicy = errors.New("unknown budget policy")

	// ErrPolicyNotImplemented indicates a recognized, reserved policy that is not
	// yet implemented in this phase.
	ErrPolicyNotImplemented = errors.New("budget policy not implemented")
)
