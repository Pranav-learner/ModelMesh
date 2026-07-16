package resilience

// State is a circuit breaker state.
type State int

const (
	// StateClosed is the normal state: requests pass through and failures are
	// counted. Reaching the failure threshold trips the breaker to Open.
	StateClosed State = iota
	// StateOpen rejects all requests immediately (fast-fail). After the cooldown
	// it transitions to Half-Open to probe recovery.
	StateOpen
	// StateHalfOpen admits a limited number of probe requests. Enough successes
	// close the breaker; any failure re-opens it.
	StateHalfOpen
)

// String returns the lowercase state name.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}
