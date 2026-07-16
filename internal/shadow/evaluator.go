package shadow

import (
	"context"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Comparison bundles a primary/shadow pair for evaluation. It is the input the
// shadow Manager hands to an Evaluator when a shadow execution completes. The
// shadow package defines it (so the Manager stays independent of the evaluation
// package); the evaluation engine consumes it.
type Comparison struct {
	// CorrelationID ties the pair back to the primary request.
	CorrelationID string
	// Primary and Shadow are the two targets.
	Primary Target
	Shadow  Target
	// PrimaryResponse / PrimaryLatency are the primary outcome to compare against.
	PrimaryResponse provider.ChatResponse
	PrimaryLatency  time.Duration
	// ShadowResult is the recorded shadow outcome (success, response, latency, err).
	ShadowResult ShadowResult
	// Metadata carries the shadow provenance (policy, sample rate, created-at).
	Metadata ShadowMetadata
}

// Evaluator receives completed primary/shadow comparisons. It is the seam the
// Evaluation Engine implements; the Manager depends on this narrow interface, not
// on the evaluation package, so shadow stays free of that dependency. An
// implementation must not block the shadow goroutine for long or panic (the
// Manager already runs it off the primary path).
type Evaluator interface {
	Evaluate(ctx context.Context, c Comparison)
}
