package config

import (
	"fmt"
	"path"
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
	PublicURL string
	Login     string // LoginPasskey, LoginGoogle or LoginGitHub
	// User is a passkey name, a Google email or a GitHub login, without
	// the subject prefix. That user also approves unknown clients.
	User string
	// ClientID and ClientSecretFile configure Google or GitHub sign-in.
	ClientID         string
	ClientSecretFile string
	// Write also grants ops:write; write tools still need targets listed.
	Write          bool
	Namespaces     []string
	DenyNamespaces []string
	PodLogs        bool
	// NoteVault enables read-only notes when set.
	NoteVault string
	// Recover lists namespace/name globs of workloads the user may restart
	// or roll back from a phone. It grants ops:write and turns on the
	// alert → push → choices → approval loop on both processes.
	Recover []string
	// Language is the notification language, en or ko.
	Language string
}

// Phone recovery wiring shared by the server and watch files.
const (
	InboxKeyFile   = "/etc/opsgate/inbox/key"
	InboxSecret    = "opsgate-inbox-hmac"
	inClusterInbox = "http://opsgate.opsgate.svc:8787"
)

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
	if o.User == "" || strings.ContainsFunc(o.User, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return nil, fmt.Errorf("user must be a single word without spaces")
	}
	for _, target := range o.Recover {
		// The server checks write allowlists only when it starts.
		ns, name, ok := strings.Cut(target, "/")
		if _, err := path.Match(target, ""); !ok || ns == "" || name == "" || strings.Contains(name, "/") || len(target) > 512 || err != nil {
			return nil, fmt.Errorf("recover targets must be namespace/name patterns like apps/* (got %q)", target)
		}
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
	w("# mode kubernetes: serves HTTP on 0.0.0.0:8787, uses the pod's ServiceAccount,")
	w("# keeps sign-in state in /var/lib/opsgate and audits to stderr.")
	w("mode: %s", ModeKubernetes)
	w("http:")
	w("  public_url: %s", q(o.PublicURL))
	w("auth:")
	w("  builtin:")
	w("    users:")
	w("      - subject: %s", q(o.Subject()))
	if o.Write || len(o.Recover) > 0 {
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
	w("  kubernetes:")
	w("    namespaces: %s", list(o.Namespaces))
	w("    deny_namespaces: %s", list(o.DenyNamespaces))
	w("    allow_logs: %t", o.PodLogs)
	if len(o.Recover) > 0 {
		w("    # Phone recovery: restart these workloads or restore a Deployment's last")
		w("    # working image. Each still needs your passkey approval.")
		w("    rollout_restart: %s", list(o.Recover))
		w("    restore_image: %s", list(o.Recover))
		w("    action_approval_url: %s", q(o.PublicURL))
	} else {
		w("    # Write tools stay off until targets are listed, e.g. rollout_restart: [\"apps/*\"]")
	}
	if o.NoteVault != "" {
		w("  note:")
		w("    enabled: true")
		w("    vault: %s", q(o.NoteVault))
	}
	if len(o.Recover) > 0 {
		lang, err := initLanguage(o.Language)
		if err != nil {
			return nil, err
		}
		w("approval:")
		w("  # Push approvals, alerts and recovery results to your phone or PC.")
		w("  push:")
		w("    enabled: true")
		w("    subject: %s", q(o.PublicURL))
		w("    language: %s", lang)
		w("  # Alerts from opsgate watch, signed with the %s Secret.", InboxSecret)
		w("  inbox_notify:")
		w("    secret_file: %s", InboxKeyFile)
		w("    subjects: [%s]", q(o.Subject()))
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

func initLanguage(lang string) (string, error) {
	switch lang {
	case "", "en":
		return "en", nil
	case "ko":
		return "ko", nil
	}
	return "", fmt.Errorf("language must be en or ko")
}

// GenerateWatch writes the `opsgate watch` configuration that pairs with a
// phone-recovery server configuration: warnings go to the approval inbox and
// push, with links to the choices page.
func GenerateWatch(o InitOptions) ([]byte, error) {
	if len(o.Recover) == 0 {
		return nil, fmt.Errorf("a watch configuration needs workloads to recover")
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
	w("# Written by `opsgate init` for `opsgate watch` (the opsgate-watch ConfigMap).")
	w("providers:")
	w("  kubernetes:")
	w("    enabled: true")
	w("    kubeconfig: \"\"   # in-cluster ServiceAccount")
	w("    namespaces: %s", list(o.Namespaces))
	w("    deny_namespaces: %s", list(o.DenyNamespaces))
	w("watch:")
	w("  # Choices links in alerts open the public server.")
	w("  approval_url: %s", q(o.PublicURL))
	w("  # Warnings reach the approval inbox and your phone through the in-cluster")
	w("  # Service, signed with the %s Secret.", InboxSecret)
	w("  inbox:")
	w("    url: %s", inClusterInbox)
	w("    secret_file: %s", InboxKeyFile)
	w("    allow_private_http: true")
	w("  problems_only: true")
	out := []byte(b.String())
	cfg, err := Parse(out, "generated watch configuration")
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateWatch(); err != nil {
		return nil, err
	}
	return out, nil
}
