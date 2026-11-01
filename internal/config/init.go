package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Sign-in choices offered by `opsgate init`.
const (
	LoginPasskey = "passkey"
	LoginGoogle  = "google"
	LoginGitHub  = "github"
)

// InitOptions are the answers `opsgate init` turns into a configuration.
type InitOptions struct {
	Mode      string // ModeHost or ModeKubernetes
	PublicURL string
	Login     string // LoginPasskey, LoginGoogle or LoginGitHub
	// User is a passkey name, a Google email or a GitHub login, without
	// the subject prefix. That user also approves unknown clients.
	User string
	// ClientID and ClientSecretFile configure Google or GitHub sign-in.
	ClientID         string
	ClientSecretFile string
	// Write also grants ops:write; write tools still need targets listed.
	Write bool
	// Kubernetes is always on in mode kubernetes. In mode host it is on
	// when Kubeconfig is set.
	Kubeconfig     string
	Namespaces     []string
	DenyNamespaces []string
	PodLogs        bool
	// Units are systemd unit globs for host tools (mode host).
	Units []string
	// NoteVault enables read-only notes when set.
	NoteVault string
}

// Subject is the configured identity of the initial user.
func (o InitOptions) Subject() string {
	switch o.Login {
	case LoginGoogle:
		return "email:" + o.User
	case LoginGitHub:
		return "github:" + o.User
	}
	return "passkey:" + o.User
}

// Generate writes a short configuration that relies on the mode for
// everything else, and checks it the way the server will.
func Generate(o InitOptions) ([]byte, error) {
	if o.Mode != ModeHost && o.Mode != ModeKubernetes {
		return nil, fmt.Errorf("mode must be %s or %s", ModeHost, ModeKubernetes)
	}
	if o.User == "" || strings.ContainsFunc(o.User, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return nil, fmt.Errorf("user must be a single word without spaces")
	}
	q := strconv.Quote
	list := func(items []string) string {
		quoted := make([]string, len(items))
		for i, item := range items {
			quoted[i] = q(item)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("# Written by `opsgate init`. Every effective value, defaults included:")
	w("#   opsgate config show -config <this file>")
	if o.Mode == ModeKubernetes {
		w("# mode kubernetes: serves HTTP on 0.0.0.0:8787, uses the pod's ServiceAccount,")
		w("# keeps sign-in state in /var/lib/opsgate, audits to stderr, no host tools.")
	} else {
		w("# mode host: serves HTTP on 127.0.0.1:8787 behind your TLS proxy, keeps sign-in")
		w("# state in /var/lib/opsgate and audits to /var/log/opsgate/audit.jsonl.")
	}
	w("mode: %s", o.Mode)
	w("http:")
	w("  public_url: %s", q(o.PublicURL))
	w("auth:")
	w("  builtin:")
	w("    users:")
	w("      - subject: %s", q(o.Subject()))
	if o.Write {
		w("        scopes: [ops:read, ops:write]")
	} else {
		w("        scopes: [ops:read]")
	}
	w("    admins: [%s]", q(o.Subject()))
	w("    # Other clients wait for an admin at <public_url>/admin/clients.")
	w("    allowed_redirect_hosts: [claude.ai, chatgpt.com, localhost]")
	w("    unknown_redirect_hosts: approve")
	switch o.Login {
	case LoginPasskey:
	case LoginGoogle, LoginGitHub:
		if o.ClientID == "" || o.ClientSecretFile == "" {
			return nil, fmt.Errorf("%s sign-in needs a client ID and a client secret file", o.Login)
		}
		w("    upstream:")
		if o.Login == LoginGoogle {
			w("      provider: oidc")
			w("      issuer: https://accounts.google.com")
		} else {
			w("      provider: github")
		}
		w("      client_id: %s", q(o.ClientID))
		w("      client_secret_file: %s", q(o.ClientSecretFile))
	default:
		return nil, fmt.Errorf("login must be %s, %s or %s", LoginPasskey, LoginGoogle, LoginGitHub)
	}
	w("providers:")
	if o.Mode == ModeHost {
		w("  host:")
		w("    units: %s", list(o.Units))
	}
	if o.Mode == ModeKubernetes || o.Kubeconfig != "" {
		w("  kubernetes:")
		if o.Mode == ModeHost {
			w("    enabled: true")
			w("    kubeconfig: %s", q(o.Kubeconfig))
		}
		w("    namespaces: %s", list(o.Namespaces))
		w("    deny_namespaces: %s", list(o.DenyNamespaces))
		w("    allow_logs: %t", o.PodLogs)
		w("    # Write tools stay off until targets are listed, e.g. rollout_restart: [\"apps/*\"]")
	}
	if o.NoteVault != "" {
		w("  note:")
		w("    enabled: true")
		w("    vault: %s", q(o.NoteVault))
	}
	out := []byte(b.String())
	cfg, err := Parse(out, "generated configuration")
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateHTTP(); err != nil {
		return nil, err
	}
	return out, nil
}
