package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// DevRunner is the configuration of `opsgate dev-runner`, kept in its own
// file that the opsgate server cannot write.
type DevRunner struct {
	// Admins are exact caller subjects allowed to read and cancel every task.
	Admins []string `yaml:"admins"`
	// Socket is the Unix socket to listen on (mode 0660).
	Socket string `yaml:"socket"`
	// SocketGroup is the group allowed to connect (the opsgate server's).
	SocketGroup string `yaml:"socket_group"`
	// WorkDir holds task worktrees, logs and results.
	WorkDir  string       `yaml:"work_dir"`
	Projects []DevProject `yaml:"projects"`
	// Agents maps an agent name (e.g. "codex", "claude") to its command.
	Agents map[string]DevAgent `yaml:"agents"`
	// Timeout bounds one task.
	Timeout       time.Duration `yaml:"timeout"`
	MaxConcurrent int           `yaml:"max_concurrent"`
	// MaxTasks bounds retained task records; the oldest finished go first.
	MaxTasks int `yaml:"max_tasks"`
}

// DevProject is a git repository agents may work on.
type DevProject struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	// BaseRef is the ref new task branches start from (default HEAD).
	BaseRef string `yaml:"base_ref"`
}

// DevAgent is an agent command. Command is argv; the runner substitutes
// {workdir} (the task worktree) and {output} (a file for the final
// message). The task prompt is written to stdin, never to argv.
type DevAgent struct {
	Command []string `yaml:"command"`
	// Env holds extra KEY=VALUE pairs; the environment is otherwise minimal.
	Env []string `yaml:"env"`
}

// LoadDevRunner reads a strict, single-document runner configuration.
func LoadDevRunner(path string) (DevRunner, error) {
	c := DevRunner{Timeout: time.Hour, MaxConcurrent: 1, MaxTasks: 100}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("parse runner configuration: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("runner configuration must contain one YAML document")
	}
	for i := range c.Projects {
		if c.Projects[i].BaseRef == "" {
			c.Projects[i].BaseRef = "HEAD"
		}
	}
	return c, c.Validate()
}

// Validate checks the runner's allowlists and resource limits.
func (c DevRunner) Validate() error {
	admins := make(map[string]bool, len(c.Admins))
	for _, subject := range c.Admins {
		if subject == "" || len(subject) > 256 || !utf8.ValidString(subject) || strings.TrimSpace(subject) != subject || strings.ContainsFunc(subject, unicode.IsControl) || admins[subject] {
			return fmt.Errorf("runner admins must be unique nonempty subjects of at most 256 bytes without control characters or surrounding whitespace")
		}
		admins[subject] = true
	}
	name := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	envName := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	clean := func(s string) bool { return !strings.ContainsFunc(s, unicode.IsControl) }
	for _, p := range []string{c.Socket, c.WorkDir} {
		if !filepath.IsAbs(p) || !clean(p) {
			return fmt.Errorf("runner socket and work_dir must be absolute paths without control characters")
		}
	}
	if c.SocketGroup == "" || !clean(c.SocketGroup) {
		return fmt.Errorf("socket_group is required")
	}
	if c.Timeout <= 0 || c.MaxConcurrent < 1 || c.MaxConcurrent > 8 || c.MaxTasks < 1 {
		return fmt.Errorf("timeout and max_tasks must be positive; max_concurrent must be 1..8")
	}
	seen := map[string]bool{}
	for _, p := range c.Projects {
		if !name.MatchString(p.Name) || seen[p.Name] {
			return fmt.Errorf("project names must be unique lowercase names of at most 63 characters")
		}
		seen[p.Name] = true
		if !filepath.IsAbs(p.Path) || !clean(p.Path) {
			return fmt.Errorf("project paths must be absolute without control characters")
		}
		if len(p.BaseRef) > 256 || strings.HasPrefix(p.BaseRef, "-") || !clean(p.BaseRef) {
			return fmt.Errorf("invalid project base_ref")
		}
	}
	for n, a := range c.Agents {
		if !name.MatchString(n) || len(a.Command) == 0 || !filepath.IsAbs(a.Command[0]) || strings.ContainsAny(a.Command[0], "{}") {
			return fmt.Errorf("invalid agent name or executable; command[0] must be an absolute path")
		}
		for _, arg := range a.Command {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return fmt.Errorf("invalid agent argument")
			}
		}
		seenEnv := map[string]bool{}
		for _, env := range a.Env {
			key, _, ok := strings.Cut(env, "=")
			if !ok || !envName.MatchString(key) || strings.ContainsRune(env, 0) || seenEnv[key] {
				return fmt.Errorf("invalid or duplicate agent environment key")
			}
			seenEnv[key] = true
		}
	}
	return nil
}
