package resilience

import "time"

// EventType identifies a health event.
type EventType int

const (
	// EventStateChanged is emitted whenever a provider's breaker state changes.
	EventStateChanged EventType = iota
	// EventProviderDown is emitted when a provider's breaker opens (goes down).
	EventProviderDown
	// EventProviderRecovered is emitted when a provider's breaker closes after
	// having been unhealthy.
	EventProviderRecovered
)

// String returns the event type name.
func (t EventType) String() string {
	switch t {
	case EventStateChanged:
		return "state_changed"
	case EventProviderDown:
		return "provider_down"
	case EventProviderRecovered:
		return "provider_recovered"
	default:
		return "unknown"
	}
}

// Event is a health event describing a provider's state change.
type Event struct {
	Type     EventType `json:"type"`
	Provider string    `json:"provider"`
	From     State     `json:"from"`
	To       State     `json:"to"`
	At       time.Time `json:"at"`
}

// Listener receives health events. Listeners are invoked synchronously from the
// monitor's probe goroutines, so they must be fast and safe for concurrent use.
type Listener func(Event)
