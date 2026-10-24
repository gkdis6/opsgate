// Package config loads the opsgate YAML configuration.
//
// Every field has a safe default: providers are disabled, scopes are
// read-only, and nothing is allowed unless it is listed explicitly.
package config

import (
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Scopes understood by opsgate.
const (
	ScopeRead  = "ops:read"
	ScopeWrite = "ops:write"
)

type Config struct {
	Server    Server    `yaml:"server"`
	Stdio     Stdio     `yaml:"stdio"`
	Providers Providers `yaml:"providers"`
}

type Server struct {
	// Name reported to MCP clients.
	Name string `yaml:"name"`
	// AuditLog is a JSONL file path. Empty means audit events go to stderr.
	AuditLog string `yaml:"audit_log"`
	// MaxOutputBytes caps the text returned by a single tool call.
	MaxOutputBytes int `yaml:"max_output_bytes"`
	// ToolTimeout bounds a single tool call.
	ToolTimeout time.Duration `yaml:"tool_timeout"`
}

// Stdio describes the identity used for the stdio transport, where the
// operating system user who launched the process is the trust boundary.
type Stdio struct {
	User   string   `yaml:"user"`
	Scopes []string `yaml:"scopes"`
}

type Providers struct {
	Host       Host       `yaml:"host"`
	Kubernetes Kubernetes `yaml:"kubernetes"`
}

type Host struct {
	Enabled bool `yaml:"enabled"`
	// Units is a glob allowlist of systemd units whose status and logs
	// may be read (e.g. "k3s.service", "nginx*"). Empty allows none.
	Units []string `yaml:"units"`
	// DiskPaths are mount points reported by host_overview.
	DiskPaths []string `yaml:"disk_paths"`
	// MaxLogLines caps journal lines per call.
	MaxLogLines int `yaml:"max_log_lines"`
}

type Kubernetes struct {
	Enabled bool `yaml:"enabled"`
	// Kubeconfig path. Empty uses in-cluster config, then $KUBECONFIG,
	// then ~/.kube/config.
	Kubeconfig string `yaml:"kubeconfig"`
	Context    string `yaml:"context"`
	// Namespaces is a glob allowlist. Empty allows none; "*" allows all.
	Namespaces []string `yaml:"namespaces"`
	// DenyNamespaces wins over Namespaces.
	DenyNamespaces []string `yaml:"deny_namespaces"`
	// AllowLogs enables k8s_logs.
	AllowLogs bool `yaml:"allow_logs"`
	// ShowConfigMapData returns ConfigMap values; otherwise only keys.
	ShowConfigMapData bool `yaml:"show_configmap_data"`
	MaxLogLines       int  `yaml:"max_log_lines"`
}

// Default returns the configuration used when no file is given.
func Default() Config {
	return Config{
		Server: Server{
			Name:           "opsgate",
			MaxOutputBytes: 64 << 10,
			ToolTimeout:    30 * time.Second,
		},
		Stdio: Stdio{
			User:   "local",
			Scopes: []string{ScopeRead},
		},
		Providers: Providers{
			Host: Host{
				DiskPaths:   []string{"/"},
				MaxLogLines: 500,
			},
			Kubernetes: Kubernetes{
				MaxLogLines: 500,
			},
		},
	}
}

// Load reads path on top of Default. Unknown keys are an error so that
// typos in security-relevant settings do not silently fall back.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Server.MaxOutputBytes <= 0 {
		return fmt.Errorf("server.max_output_bytes must be positive")
	}
	if c.Server.ToolTimeout <= 0 {
		return fmt.Errorf("server.tool_timeout must be positive")
	}
	for _, s := range c.Stdio.Scopes {
		if s != ScopeRead && s != ScopeWrite {
			return fmt.Errorf("stdio.scopes: unknown scope %q", s)
		}
	}
	return nil
}
