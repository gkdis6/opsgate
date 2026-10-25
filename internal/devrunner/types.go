package devrunner

import "time"

const (
	// MaxPromptBytes bounds a task's requirement.
	MaxPromptBytes = 32 << 10
	// MaxReadBytes bounds a log or diff response before JSON encoding.
	MaxReadBytes = 256 << 10
	maxLogBytes  = 20 << 20
)

// Project is an allowlisted project, without its filesystem path.
type Project struct {
	Name    string `json:"name"`
	BaseRef string `json:"base_ref"`
}

// Info is the runner's public policy and capacity.
type Info struct {
	Projects       []Project     `json:"projects"`
	Agents         []string      `json:"agents"`
	Timeout        time.Duration `json:"timeout"`
	MaxConcurrent  int           `json:"max_concurrent"`
	MaxTasks       int           `json:"max_tasks"`
	MaxPromptBytes int           `json:"max_prompt_bytes"`
}

// StartRequest selects configured names, never commands or paths.
type StartRequest struct {
	Project     string `json:"project"`
	Agent       string `json:"agent"`
	Prompt      string `json:"prompt"`
	RequestedBy string `json:"requested_by"`
}

// Task describes a queued or completed agent run.
type Task struct {
	owner       string
	ID          string    `json:"id"`
	Project     string    `json:"project"`
	Agent       string    `json:"agent"`
	Branch      string    `json:"branch"`
	BaseRef     string    `json:"base_ref"`
	BaseCommit  string    `json:"base_commit,omitempty"`
	RequestedBy string    `json:"requested_by"`
	State       string    `json:"state"`
	Created     time.Time `json:"created"`
	Started     time.Time `json:"started,omitempty"`
	Finished    time.Time `json:"finished,omitempty"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// Log is a bounded page; NextOffset addresses the next byte in the raw log.
type Log struct {
	Text       string `json:"text"`
	NextOffset int64  `json:"next_offset"`
	Truncated  bool   `json:"truncated"`
}

// Diff contains a bounded stat and patch against the task's pinned base commit.
type Diff struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func finished(state string) bool { return state != "queued" && state != "running" }
