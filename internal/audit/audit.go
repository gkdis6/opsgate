// Package audit records every tool call as one JSON line.
package audit

import (
	"time"
)

// Event is one tool call. Args must already be redacted by the caller.
type Event struct {
	Time       time.Time      `json:"time"`
	User       string         `json:"user"`
	Transport  string         `json:"transport"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args,omitempty"`
	Outcome    string         `json:"outcome"` // "ok", "error", "denied"
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	OutputSize int            `json:"output_bytes"`
}

// Logger writes events. It is safe for concurrent use.
type Logger struct {
	// TODO(codex-C): implement.
}

// Open returns a Logger appending to path (created 0600 if missing).
// An empty path logs to stderr.
func Open(path string) (*Logger, error) {
	// TODO(codex-C): implement.
	return &Logger{}, nil
}

// Record writes e. Failures are reported to stderr, never to the caller,
// so that a full disk cannot be used to make tools fail open or closed
// unexpectedly.
func (l *Logger) Record(e Event) {
	// TODO(codex-C): implement.
}

func (l *Logger) Close() error {
	// TODO(codex-C): implement.
	return nil
}
