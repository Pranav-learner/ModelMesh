package shadow

import (
	"sync"
	"time"

	"github.com/symbiotes/modelmesh/internal/provider"
)

// Target is a provider+model destination.
type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Primary describes the primary request's outcome, passed to Shadow so the
// evaluation stage (Part 2) can compare the shadow response against it. The
// response and latency are used only for evaluation, never re-served.
type Primary struct {
	Target   Target
	Response provider.ChatResponse
	Latency  time.Duration
}

// ShadowRequest is a cloned request destined for a secondary provider. The clone
// is deep with respect to the mutable parts of a ChatRequest, so neither the
// primary nor the shadow can observe the other's mutations.
type ShadowRequest struct {
	ID      string               `json:"id"`
	Request provider.ChatRequest `json:"request"`
	Target  Target               `json:"target"`
}

// ShadowMetadata describes the provenance of a shadow execution: which primary
// request it mirrors and how it was sampled. It carries no response data.
type ShadowMetadata struct {
	// CorrelationID ties the shadow back to the primary request (its request ID).
	CorrelationID string `json:"correlation_id,omitempty"`
	// Primary is the provider+model the primary request used.
	Primary Target `json:"primary"`
	// Policy is the sampling policy that selected this request.
	Policy string `json:"policy"`
	// SampleRate is the effective sampling percentage in [0,100].
	SampleRate float64 `json:"sample_rate"`
	// CreatedAt is when the shadow was created.
	CreatedAt time.Time `json:"created_at"`
}

// ShadowResult is the recorded outcome of a shadow execution. It is never returned
// to the application; it exists only for evaluation. Errors are captured as
// strings so a result is serializable and a failure is fully contained.
type ShadowResult struct {
	Response    provider.ChatResponse `json:"response"`
	Success     bool                  `json:"success"`
	Err         string                `json:"error,omitempty"`
	Latency     time.Duration         `json:"latency"`
	StartedAt   time.Time             `json:"started_at"`
	CompletedAt time.Time             `json:"completed_at"`
}

// ShadowExecution represents one sampled shadow request and its (eventual) result.
// The result is populated asynchronously; use Wait or Result to read it.
type ShadowExecution struct {
	ID       string         `json:"id"`
	Request  ShadowRequest  `json:"request"`
	Metadata ShadowMetadata `json:"metadata"`

	// primary is the primary outcome to evaluate against; unexported so it is not
	// serialized and never leaks into the recorded metadata.
	primary Primary

	mu     sync.Mutex
	done   chan struct{}
	result ShadowResult
}

// newExecution constructs an execution with an open done channel.
func newExecution(id string, req ShadowRequest, meta ShadowMetadata) *ShadowExecution {
	return &ShadowExecution{ID: id, Request: req, Metadata: meta, done: make(chan struct{})}
}

// complete records the result and signals completion exactly once.
func (e *ShadowExecution) complete(r ShadowResult) {
	e.mu.Lock()
	e.result = r
	e.mu.Unlock()
	close(e.done)
}

// Done returns a channel closed when the execution completes.
func (e *ShadowExecution) Done() <-chan struct{} { return e.done }

// Wait blocks until the execution completes and returns its result.
func (e *ShadowExecution) Wait() ShadowResult {
	<-e.done
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.result
}

// Result returns the result and whether the execution has completed.
func (e *ShadowExecution) Result() (ShadowResult, bool) {
	select {
	case <-e.done:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.result, true
	default:
		return ShadowResult{}, false
	}
}

// cloneRequest returns a deep copy of the mutable parts of a ChatRequest, so the
// shadow and primary are fully isolated from each other's mutations.
func cloneRequest(req provider.ChatRequest) provider.ChatRequest {
	out := req // copies scalar fields

	if req.Messages != nil {
		out.Messages = make([]provider.ChatMessage, len(req.Messages))
		copy(out.Messages, req.Messages)
	}
	if req.Stop != nil {
		out.Stop = make([]string, len(req.Stop))
		copy(out.Stop, req.Stop)
	}
	if req.Metadata != nil {
		out.Metadata = make(map[string]string, len(req.Metadata))
		for k, v := range req.Metadata {
			out.Metadata[k] = v
		}
	}
	if req.Temperature != nil {
		t := *req.Temperature
		out.Temperature = &t
	}
	if req.TopP != nil {
		p := *req.TopP
		out.TopP = &p
	}
	return out
}
