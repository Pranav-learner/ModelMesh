package routing

import "errors"

// Sentinel errors for the routing framework. Callers match with errors.Is.
var (
	// ErrNoCandidates indicates no provider/model satisfied the routing context
	// (after capability filtering and constraints). It is the routing analogue of
	// "no healthy provider" that later phases will refine with health awareness.
	ErrNoCandidates = errors.New("no routing candidates")

	// ErrUnknownStrategy indicates a request for a strategy name that has no
	// registered builder and is not a known-reserved name.
	ErrUnknownStrategy = errors.New("unknown routing strategy")

	// ErrStrategyNotImplemented indicates a strategy that is a recognized,
	// reserved extension point but not yet implemented in this phase.
	ErrStrategyNotImplemented = errors.New("routing strategy not implemented")

	// ErrInvalidRoutingConfig indicates a structurally invalid routing config.
	ErrInvalidRoutingConfig = errors.New("invalid routing config")

	// ErrNoValidProvider indicates that candidates were ranked but every one
	// failed provider validation, so none could be selected (even after fallback).
	ErrNoValidProvider = errors.New("no valid provider after fallback")
)
