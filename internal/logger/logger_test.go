package logger

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLogger_WritesStructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, LevelInfo)

	log.Info("provider registered", String("provider", "openai"), Int("count", 1))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\n%s", err, buf.String())
	}
	if entry["msg"] != "provider registered" {
		t.Errorf("msg = %v, want 'provider registered'", entry["msg"])
	}
	if entry["provider"] != "openai" {
		t.Errorf("provider field = %v, want openai", entry["provider"])
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, LevelWarn)

	log.Debug("debug msg")
	log.Info("info msg")
	if buf.Len() != 0 {
		t.Errorf("entries below the configured level were emitted: %s", buf.String())
	}

	log.Warn("warn msg")
	if !strings.Contains(buf.String(), "warn msg") {
		t.Errorf("warn entry not emitted at LevelWarn")
	}
}

func TestLogger_With(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, LevelInfo).With(String("component", "provider"))

	log.Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if entry["component"] != "provider" {
		t.Errorf("With() context not attached: %v", entry)
	}
}

func TestLogger_ErrField(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, LevelError)
	log.Error("boom", Err(errors.New("kaboom")))

	if !strings.Contains(buf.String(), "kaboom") {
		t.Errorf("Err() field not rendered: %s", buf.String())
	}
}

func TestNopLogger_DoesNotPanicOrWrite(t *testing.T) {
	log := Nop()
	// Must be safe to call and to derive from.
	log.Debug("x")
	log.Info("x")
	log.Warn("x")
	log.Error("x")
	child := log.With(String("k", "v"))
	child.Info("y")
	// No assertions: the test passes if nothing panics.
}
