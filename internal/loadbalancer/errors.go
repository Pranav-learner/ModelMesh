package loadbalancer

import "errors"

// Sentinel errors for the load balancer. Callers match with errors.Is.
var (
	// ErrNoInstances indicates no eligible instance was available to serve the
	// request (none registered, or all disabled/unhealthy, or none matched the
	// request's provider filter).
	ErrNoInstances = errors.New("no eligible load balancer instances")

	// ErrInstanceExists indicates registering an instance whose ID is already
	// registered.
	ErrInstanceExists = errors.New("instance already registered")

	// ErrInstanceNotFound indicates an operation referenced an unknown instance ID.
	ErrInstanceNotFound = errors.New("instance not found")

	// ErrInvalidInstance indicates an instance missing a required field (ID or
	// Provider).
	ErrInvalidInstance = errors.New("invalid instance")

	// ErrUnknownStrategy indicates a strategy name with no registered builder and
	// which is not a known-reserved name.
	ErrUnknownStrategy = errors.New("unknown load balancing strategy")

	// ErrStrategyNotImplemented indicates a recognized, reserved strategy that is
	// not yet implemented in this phase.
	ErrStrategyNotImplemented = errors.New("load balancing strategy not implemented")

	// ErrInvalidConfig indicates a structurally invalid load balancer config.
	ErrInvalidConfig = errors.New("invalid load balancer config")
)
