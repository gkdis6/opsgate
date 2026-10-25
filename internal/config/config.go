// Package config loads the opsgate YAML configuration.
//
// Every field has a safe default: providers are disabled, scopes are
// read-only, and nothing is allowed unless it is listed explicitly.
package config

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Scopes understood by opsgate.
const (
	ScopeRead  = "ops:read"
	ScopeWrite = "ops:write"
)

// Auth modes for the HTTP transport.
const (
	AuthBuiltin  = "builtin"
	AuthExternal = "external"
	AuthStatic   = "static"
)

type Config struct {
	Server    Server    `yaml:"server"`
	Stdio     Stdio     `yaml:"stdio"`
	HTTP      HTTP      `yaml:"http"`
	Auth      Auth      `yaml:"auth"`
	Approval  Approval  `yaml:"approval"`
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

// HTTP configures the Streamable HTTP transport.
type HTTP struct {
	// Listen is the TCP address, e.g. "127.0.0.1:8787".
	Listen string `yaml:"listen"`
	// PublicURL is the externally visible origin, e.g.
	// "https://mcp.example.com". The MCP endpoint is PublicURL + "/mcp"
	// and the built-in authorization server's issuer is PublicURL.
	PublicURL string `yaml:"public_url"`
}

// Auth configures how HTTP requests are authenticated.
type Auth struct {
	// Mode is one of builtin, external, static. Required for HTTP.
	Mode     string        `yaml:"mode"`
	Builtin  BuiltinAuth   `yaml:"builtin"`
	External ExternalAuth  `yaml:"external"`
	Static   []StaticToken `yaml:"static"`
}

// User grants scopes to one upstream identity. Subject is
// "github:<login>" or "email:<verified email>".
type User struct {
	Subject string   `yaml:"subject"`
	Scopes  []string `yaml:"scopes"`
}

// BuiltinAuth configures the embedded OAuth 2.1 authorization server.
type BuiltinAuth struct {
	// StateFile persists registered clients and token hashes (mode 0600).
	StateFile string `yaml:"state_file"`
	// Users allowed to sign in. Anyone else is rejected after upstream login.
	Users           []User        `yaml:"users"`
	Upstream        Upstream      `yaml:"upstream"`
	AccessTokenTTL  time.Duration `yaml:"access_token_ttl"`
	RefreshTokenTTL time.Duration `yaml:"refresh_token_ttl"`
	// MaxClients bounds Dynamic Client Registration.
	MaxClients int `yaml:"max_clients"`
}

// Upstream is the identity provider that authenticates people.
type Upstream struct {
	// Provider is "github" or "oidc" (Google: issuer https://accounts.google.com).
	Provider string `yaml:"provider"`
	// Issuer is required for oidc.
	Issuer   string `yaml:"issuer"`
	ClientID string `yaml:"client_id"`
	// ClientSecretFile holds the client secret; ClientSecretEnv names an
	// environment variable instead. Secrets are never written in YAML.
	ClientSecretFile string `yaml:"client_secret_file"`
	ClientSecretEnv  string `yaml:"client_secret_env"`
}

// ExternalAuth validates JWT access tokens from an existing authorization
// server via its JWKS.
type ExternalAuth struct {
	Issuer string `yaml:"issuer"`
	// Audience defaults to the MCP resource URL (PublicURL + "/mcp").
	Audience string `yaml:"audience"`
	// UserClaim names the claim used as the user identity (default "sub").
	UserClaim string `yaml:"user_claim"`
	// Users, if set, restricts and maps identities like BuiltinAuth.Users
	// with Subject "claim:<value>". Empty means scopes come from the token.
	Users []User `yaml:"users"`
}

// StaticToken is a pre-shared bearer token for local HTTP clients.
type StaticToken struct {
	Name string `yaml:"name"`
	// SHA256 is the hex SHA-256 of the token; the token itself is never stored.
	SHA256 string   `yaml:"sha256"`
	Scopes []string `yaml:"scopes"`
}

// Approval configures human confirmation of write tools.
type Approval struct {
	// TTL bounds how long a pending web approval stays valid.
	TTL time.Duration `yaml:"ttl"`
	// Web enables browser approval links for clients without elicitation.
	// Requires auth.mode builtin (the approver signs in upstream).
	Web bool `yaml:"web"`
	// RequireWeb disables in-client elicitation, including for stdio.
	RequireWeb bool `yaml:"require_web"`
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
	// RestartUnits is a glob allowlist for host_service_restart. Empty
	// disables the tool.
	RestartUnits []string `yaml:"restart_units"`
	// CommandPrefix is prepended to write commands, e.g. ["/usr/bin/sudo", "-n"].
	// Elements must be absolute paths or flags.
	CommandPrefix []string `yaml:"command_prefix"`
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
	// Write actions. Each list holds "namespace/name" globs; empty
	// disables the tool. Namespace allow/deny rules still apply.
	RolloutRestart []string `yaml:"rollout_restart"`
	Scale          []string `yaml:"scale"`
	DeletePod      []string `yaml:"delete_pod"`
	// MaxReplicas caps k8s_scale.
	MaxReplicas int32 `yaml:"max_replicas"`
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
		HTTP: HTTP{
			Listen: "127.0.0.1:8787",
		},
		Auth: Auth{
			Builtin: BuiltinAuth{
				AccessTokenTTL:  time.Hour,
				RefreshTokenTTL: 30 * 24 * time.Hour,
				MaxClients:      100,
			},
			External: ExternalAuth{
				UserClaim: "sub",
			},
		},
		Approval: Approval{
			TTL: 10 * time.Minute,
		},
		Providers: Providers{
			Host: Host{
				DiskPaths:   []string{"/"},
				MaxLogLines: 500,
			},
			Kubernetes: Kubernetes{
				MaxLogLines: 500,
				MaxReplicas: 10,
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
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("parse %s: multiple YAML documents are not supported", path)
	}
	return cfg, cfg.Validate()
}

// Validate checks settings that do not depend on the transport.
func (c Config) Validate() error {
	if c.Server.MaxOutputBytes <= 0 {
		return fmt.Errorf("server.max_output_bytes must be positive")
	}
	if c.Server.ToolTimeout <= 0 {
		return fmt.Errorf("server.tool_timeout must be positive")
	}
	if err := checkScopes("stdio.scopes", c.Stdio.Scopes); err != nil {
		return err
	}
	for _, u := range append(append([]User{}, c.Auth.Builtin.Users...), c.Auth.External.Users...) {
		if err := checkScopes("auth users "+u.Subject, u.Scopes); err != nil {
			return err
		}
	}
	for _, t := range c.Auth.Static {
		if err := checkScopes("auth.static "+t.Name, t.Scopes); err != nil {
			return err
		}
	}
	for i, p := range c.Providers.Host.CommandPrefix {
		if i == 0 && !filepath.IsAbs(p) {
			return fmt.Errorf("providers.host.command_prefix[0] must be an absolute executable path")
		}
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "-") {
			return fmt.Errorf("providers.host.command_prefix: %q must be an absolute path or a flag", p)
		}
	}
	if c.Providers.Kubernetes.MaxReplicas < 0 {
		return fmt.Errorf("providers.kubernetes.max_replicas must not be negative")
	}
	return nil
}

// ValidateHTTP checks the settings required to serve over HTTP. It refuses
// configurations that would expose tools without authentication.
func (c Config) ValidateHTTP() error {
	if err := c.Validate(); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(c.HTTP.Listen)
	if err != nil {
		return fmt.Errorf("http.listen: %w", err)
	}
	u, err := url.Parse(c.HTTP.PublicURL)
	if err != nil || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("http.public_url must be an origin like https://mcp.example.com")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("http.public_url must use https (http is allowed only for localhost)")
	}
	switch c.Auth.Mode {
	case AuthBuiltin, AuthExternal, AuthStatic:
	case "":
		return fmt.Errorf("auth.mode is required for the HTTP transport (listen %s)", host)
	default:
		return fmt.Errorf("auth.mode: unknown mode %q", c.Auth.Mode)
	}
	if c.Approval.RequireWeb && !c.Approval.Web {
		return fmt.Errorf("approval.require_web requires approval.web")
	}
	if c.Approval.Web && c.Auth.Mode != AuthBuiltin {
		return fmt.Errorf("approval.web requires auth.mode builtin")
	}
	return nil
}

// ResourceURL is the MCP endpoint and OAuth resource identifier.
func (c Config) ResourceURL() string {
	return strings.TrimSuffix(c.HTTP.PublicURL, "/") + "/mcp"
}

// Issuer is the built-in authorization server's issuer identifier.
func (c Config) Issuer() string {
	return strings.TrimSuffix(c.HTTP.PublicURL, "/")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func checkScopes(where string, scopes []string) error {
	for _, s := range scopes {
		if s != ScopeRead && s != ScopeWrite {
			return fmt.Errorf("%s: unknown scope %q", where, s)
		}
	}
	return nil
}
