// Package audit records every tool call as one JSON line.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// Event is one tool call. Args must already be redacted by the caller.
type Event struct {
	Time      time.Time      `json:"time"`
	User      string         `json:"user"`
	Transport string         `json:"transport"`
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args,omitempty"`
	Outcome   string         `json:"outcome"` // "ok", "error", "denied", "pending"
	Error     string         `json:"error,omitempty"`
	// Write tools only: the plan shown to the human and how it was approved.
	Plan       string `json:"plan,omitempty"`
	Approval   string `json:"approval,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	OutputSize int    `json:"output_bytes"`
}

// Logger writes events. It is safe for concurrent use.
type Logger struct {
	mu    sync.Mutex
	file  *os.File
	owned bool
}

// Open returns a Logger appending to path (created 0600 if missing).
// An empty path logs to stderr.
func Open(path string) (*Logger, error) {
	if path == "" {
		return &Logger{file: os.Stderr}, nil
	}
	// O_NOFOLLOW closes the symlink race between checking and opening.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		f.Close()
		return nil, fmt.Errorf("audit log must be a regular file without group/world write permission")
	}
	return &Logger{file: f, owned: true}, nil
}

// Record writes e. Failures are reported to stderr, never to the caller,
// so that a full disk cannot be used to make tools fail open or closed
// unexpectedly.
func (l *Logger) Record(e Event) {
	// An invalid event (including a panicking JSON marshaler) must not crash a tool.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "opsgate: audit record panic")
		}
	}()
	if l == nil {
		fmt.Fprintln(os.Stderr, "opsgate: audit logger is nil")
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := json.Marshal(e)
	if err == nil {
		if l.file == nil {
			err = os.ErrClosed
		} else {
			_, err = l.file.Write(append(b, '\n'))
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "opsgate: audit record: %v\n", err)
	}
}

// Close closes an owned log file, leaving stderr open. It is safe to repeat.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.owned || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
