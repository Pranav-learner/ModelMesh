package tracing

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/symbiotes/modelmesh/internal/logger"
)

// requestIDKey is the context key for the correlation (request) ID.
type ctxKey int

const requestIDKey ctxKey = iota

// requestIDPrefix namespaces generated correlation IDs.
const requestIDPrefix = "req_"

// NewRequestID generates a random correlation ID. It uses crypto/rand and falls
// back to a fixed marker only if the system entropy source fails (which does not
// happen in practice).
func NewRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return requestIDPrefix + "0000000000000000000000000000000000000000000000000000000000000000"[:24]
	}
	return requestIDPrefix + hex.EncodeToString(b[:])
}

// WithRequestID returns a context carrying the given correlation ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the correlation ID in ctx, if present.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok && id != ""
}

// EnsureRequestID returns a context guaranteed to carry a correlation ID: the
// existing one if present, otherwise a freshly generated one. It returns the
// context and the ID.
func EnsureRequestID(ctx context.Context) (context.Context, string) {
	if id, ok := RequestIDFromContext(ctx); ok {
		return ctx, id
	}
	id := NewRequestID()
	return WithRequestID(ctx, id), id
}

// LoggerWith returns a logger enriched with correlation fields from ctx:
// request_id (correlation) and trace_id/span_id (active span). If neither is
// present, the original logger is returned unchanged. This is how every log entry
// becomes traceable to one request.
func LoggerWith(ctx context.Context, log logger.Logger) logger.Logger {
	var fields []logger.Field
	if id, ok := RequestIDFromContext(ctx); ok {
		fields = append(fields, logger.String("request_id", id))
	}
	if traceID, spanID, ok := SpanContextFromContext(ctx); ok {
		fields = append(fields, logger.String("trace_id", traceID), logger.String("span_id", spanID))
	}
	if len(fields) == 0 {
		return log
	}
	return log.With(fields...)
}
