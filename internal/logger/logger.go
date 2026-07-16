// Package logger provides a small, structured logging abstraction for
// ModelMesh. It intentionally hides the concrete logging backend behind a
// minimal interface so it can be swapped later (for example to add OpenTelemetry
// log correlation in the Observability phase) without touching call sites.
//
// The default implementation is backed by the standard library's log/slog. A
// no-op implementation is provided for tests and for components that must not
// depend on global logging state.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// Level is a backend-independent log level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Field is a single structured key/value pair. Using an explicit Field type
// (rather than variadic any) keeps call sites self-documenting and lets us
// change the backend without changing callers.
type Field struct {
	Key   string
	Value any
}

// Field constructors. These are deliberately tiny helpers that read well at the
// call site, e.g. logger.String("provider", name).
func Any(key string, value any) Field   { return Field{Key: key, Value: value} }
func String(key, value string) Field    { return Field{Key: key, Value: value} }
func Int(key string, value int) Field   { return Field{Key: key, Value: value} }
func Bool(key string, value bool) Field { return Field{Key: key, Value: value} }

// Err wraps an error under the conventional "error" key.
func Err(err error) Field { return Field{Key: "error", Value: err} }

// Logger is the structured logging contract used throughout ModelMesh. It is
// intentionally small: four level methods plus With for deriving a logger that
// carries additional context.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)

	// With returns a child logger that includes the given fields on every
	// subsequent log entry. The receiver is not modified.
	With(fields ...Field) Logger
}

// slogLogger is the default Logger backed by log/slog.
type slogLogger struct {
	l *slog.Logger
}

// New returns a Logger writing JSON-structured logs to stderr at or above the
// given level.
func New(level Level) Logger {
	return NewWithWriter(os.Stderr, level)
}

// NewWithWriter returns a Logger writing JSON-structured logs to w.
func NewWithWriter(w io.Writer, level Level) Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: toSlogLevel(level)})
	return &slogLogger{l: slog.New(handler)}
}

// NewWithHandler returns a Logger backed by a caller-supplied slog.Handler,
// which is the primary extension seam for swapping the backend (e.g. a handler
// that also emits OpenTelemetry logs) later.
func NewWithHandler(h slog.Handler) Logger {
	return &slogLogger{l: slog.New(h)}
}

func (s *slogLogger) Debug(msg string, fields ...Field) { s.log(LevelDebug, msg, fields) }
func (s *slogLogger) Info(msg string, fields ...Field)  { s.log(LevelInfo, msg, fields) }
func (s *slogLogger) Warn(msg string, fields ...Field)  { s.log(LevelWarn, msg, fields) }
func (s *slogLogger) Error(msg string, fields ...Field) { s.log(LevelError, msg, fields) }

func (s *slogLogger) With(fields ...Field) Logger {
	return &slogLogger{l: s.l.With(toAttrs(fields)...)}
}

func (s *slogLogger) log(level Level, msg string, fields []Field) {
	// context.Background is used because ModelMesh does not yet propagate a
	// per-request context into the logger; the Observability phase can switch to
	// a context-aware path here without changing the Logger interface.
	s.l.LogAttrs(context.Background(), toSlogLevel(level), msg, toSlogAttrs(fields)...)
}

func toSlogLevel(level Level) slog.Level {
	switch level {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func toSlogAttrs(fields []Field) []slog.Attr {
	attrs := make([]slog.Attr, len(fields))
	for i, f := range fields {
		attrs[i] = slog.Any(f.Key, f.Value)
	}
	return attrs
}

// toAttrs converts fields to the ...any form slog.Logger.With expects.
func toAttrs(fields []Field) []any {
	attrs := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		attrs = append(attrs, f.Key, f.Value)
	}
	return attrs
}

// nopLogger is a Logger that discards everything. It is the safe default for
// components that receive no logger, ensuring they never panic or depend on a
// global.
type nopLogger struct{}

// Nop returns a Logger that discards all entries.
func Nop() Logger { return nopLogger{} }

func (nopLogger) Debug(string, ...Field) {}
func (nopLogger) Info(string, ...Field)  {}
func (nopLogger) Warn(string, ...Field)  {}
func (nopLogger) Error(string, ...Field) {}
func (n nopLogger) With(...Field) Logger { return n }
