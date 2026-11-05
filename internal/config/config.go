// Package config loads the opsgate YAML configuration.
//
// Every field has a safe default: providers are disabled, scopes are
// read-only, and nothing is allowed unless it is listed explicitly.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Scopes understood by opsgate.
const (
	ScopeRead  = "ops:read"
	ScopeWrite = "ops:write"
	// ScopeDev allows starting coding agents through the dev provider.
	ScopeDev = "dev:run"
)

// Scopes lists every scope opsgate understands, in display order.
var Scopes = []string{ScopeRead, ScopeWrite, ScopeDev}

// KnownScope reports whether s is one of Scopes.
func KnownScope(s string) bool { return slices.Contains(Scopes, s) }

// Auth modes for the HTTP transport.
const (
	AuthBuiltin  = "builtin"
	AuthExternal = "external"
	AuthStatic   = "static"
)

// Deployment modes. A mode fills in the defaults for where the server runs
// as a service; values written in the file still win.
const (
	// ModeKubernetes is a pod that inspects its own cluster.
	ModeKubernetes = "kubernetes"
)

// Config holds the server configuration.
type Config struct {
	// Mode is empty (plain defaults, stdio by default) or kubernetes.
	Mode      string    `yaml:"mode"`
	Server    Server    `yaml:"server"`
	Stdio     Stdio     `yaml:"stdio"`
	HTTP      HTTP      `yaml:"http"`
	Auth      Auth      `yaml:"auth"`
	Approval  Approval  `yaml:"approval"`
	Providers Providers `yaml:"providers"`
	Watch     Watch     `yaml:"watch"`
}

// Server configures tool execution, registration and audit logging.
type Server struct {
	// Name reported to MCP clients.
	Name string `yaml:"name"`
	// AuditLog is a JSONL file path. Empty means audit events go to stderr.
	AuditLog string `yaml:"audit_log"`
	// MaxOutputBytes caps the text returned by a single tool call.
	MaxOutputBytes int `yaml:"max_output_bytes"`
	// ToolTimeout bounds a single tool call.
	ToolTimeout time.Duration `yaml:"tool_timeout"`
	// Tools decides which tools exist at all. Denied tools are not
	// registered, so they are absent from tools/list.
	Tools ToolPolicy `yaml:"tools"`
}

// ToolPolicy holds path.Match globs over tool names. An empty Allow allows
// every tool; Deny wins over Allow. Patterns that match no registered tool
// are a configuration error, so typos cannot silently widen access.
type ToolPolicy struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
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

// User grants scopes and optional tool restrictions to one identity. Subject is
// "passkey:<name>", "github:<login>", "email:<verified email>", "oidc:<sub>",
// or "claim:<value>" for the configured external JWT user claim.
type User struct {
	Subject string   `yaml:"subject"`
	Scopes  []string `yaml:"scopes"`
	// Tools optionally restricts this user to tools matching these globs,
	// on top of scopes and the server-wide policy. Empty means no extra limit.
	Tools []string `yaml:"tools"`
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
	// AllowedRedirectHosts lists redirect URI hosts that Dynamic Client
	// Registration accepts (exact host names; "localhost" also covers
	// loopback IPs and any port). Empty accepts any host.
	AllowedRedirectHosts []string `yaml:"allowed_redirect_hosts"`
	// UnknownRedirectHosts decides what happens to a registration whose
	// redirect host is not listed: "deny" (default when the list is set) or
	// "approve" (the client stays pending until an admin approves it at
	// /admin/clients). Ignored when AllowedRedirectHosts is empty.
	UnknownRedirectHosts string `yaml:"unknown_redirect_hosts"`
	// Admins are subjects allowed to approve pending clients.
	Admins []string `yaml:"admins"`
	// NotifyURL, if set, receives a JSON POST when a client awaits approval
	// (e.g. an n8n webhook that forwards it to chat).
	NotifyURL string `yaml:"notify_url"`
}

// Upstream is the identity provider that authenticates people.
type Upstream struct {
	// Provider is "passkey" (no external identity provider), "github" or
	// "oidc" (Google: issuer https://accounts.google.com).
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
	// Tools restricts this token like User.Tools.
	Tools []string `yaml:"tools"`
}

// Approval configures human confirmation of write tools.
type Approval struct {
	// TTL bounds how long a pending web approval stays valid.
	TTL time.Duration `yaml:"ttl"`
	// Web enables browser approval links.
	// Requires auth.mode builtin (the approver signs in upstream).
	Web bool `yaml:"web"`
	// RequireWeb disables in-client elicitation, including for stdio.
	RequireWeb bool `yaml:"require_web"`
	// RequireWebDefaulted records the HTTP default so stdio can allow elicitation.
	RequireWebDefaulted bool `yaml:"-"`
	// Window is the optional lifetime of a user/tool/target auto-approval grant.
	// Zero disables grants; otherwise it must be between 1 minute and 12 hours.
	Window time.Duration `yaml:"window"`
	// WindowTools is a path.Match glob allowlist. Empty permits no grants.
	WindowTools []string `yaml:"window_tools"`
}

// Providers configures the available tool providers.
type Providers struct {
	Kubernetes Kubernetes `yaml:"kubernetes"`
	Note       Note       `yaml:"note"`
	Dev        Dev        `yaml:"dev"`
}

// Dev lets clients start coding agents (Codex, Claude Code, ...) on the
// host. opsgate never runs agents itself: it talks to `opsgate dev-runner`,
// a separate process under its own account whose configuration (DevRunner)
// alone decides which projects and agent commands exist. Starting a task
// needs the dev:run scope and human approval.
type Dev struct {
	Enabled bool `yaml:"enabled"`
	// RunnerSocket is the dev-runner's Unix socket.
	RunnerSocket string `yaml:"runner_socket"`
}

// Note exposes a folder of Markdown notes (an Obsidian vault works as is)
// by reading files directly; no note app needs to run.
type Note struct {
	Enabled bool `yaml:"enabled"`
	// Vault is the absolute path of the vault root.
	Vault string `yaml:"vault"`
	// Exclude holds extra vault-relative globs that are never read or
	// written. Internal folders (.obsidian, .trash, .git, sync metadata)
	// and sync-conflict copies are always excluded.
	Exclude []string `yaml:"exclude"`
	// AllowWrite registers the note write tools (create, append, replace,
	// trash). Every write still needs ops:write and human approval.
	AllowWrite bool `yaml:"allow_write"`
	// MaxNoteBytes caps the size of a note that is read or written.
	MaxNoteBytes int `yaml:"max_note_bytes"`
}

// Load reads path on top of the defaults of its mode. Unknown keys are an
// error so that typos in security-relevant settings do not silently fall back.
func Load(path string) (Config, error) {
	if path == "" {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Default(), err
	}
	return Parse(data, path)
}

// Parse is Load for configuration already in memory; name labels errors.
func Parse(data []byte, name string) (Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", name, err)
	}
	mode, _ := lookup(&root, "mode").(string)
	cfg, err := DefaultFor(mode)
	if err != nil {
		return cfg, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("parse %s: %w", name, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("parse %s: multiple YAML documents are not supported", name)
	}
	// Browser approval needs builtin sign-in, so a mode turns it on only
	// when the file keeps builtin and does not decide itself.
	if cfg.Mode != "" && cfg.Auth.Mode == AuthBuiltin && lookup(&root, "approval", "web") == nil {
		cfg.Approval.Web = true
	}
	if cfg.Approval.Web && lookup(&root, "approval", "require_web") == nil {
		cfg.Approval.RequireWeb = true
		cfg.Approval.RequireWebDefaulted = true
	}
	if cfg.Watch.Email.Host == "" {
		if email, ok := lookup(&root, "watch", "email").(map[string]any); ok {
			for field := range email {
				if field != "host" {
					return cfg, fmt.Errorf("watch.email.%s requires watch.email.host", field)
				}
			}
		}
	}
	return cfg, cfg.Validate()
}

// lookup returns the decoded value at a mapping path, or nil when absent.
func lookup(n *yaml.Node, keys ...string) any {
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	for _, key := range keys {
		if n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	var v any
	if n.Decode(&v) != nil {
		return nil
	}
	return v
}

// ResourceURL is the MCP endpoint and OAuth resource identifier.
func (c Config) ResourceURL() string {
	return strings.TrimSuffix(c.HTTP.PublicURL, "/") + "/mcp"
}

// Issuer is the built-in authorization server's issuer identifier.
func (c Config) Issuer() string {
	return strings.TrimSuffix(c.HTTP.PublicURL, "/")
}
