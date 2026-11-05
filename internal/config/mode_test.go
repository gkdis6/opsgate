package config

import (
	"strings"
	"testing"
)

func TestModeDefaults(t *testing.T) {
	k, err := Parse([]byte("mode: kubernetes\n"), "k")
	if err != nil {
		t.Fatal(err)
	}
	if k.HTTP.Listen != "0.0.0.0:8787" || k.Auth.Mode != AuthBuiltin || k.Auth.Builtin.Upstream.Provider != "passkey" ||
		k.Auth.Builtin.StateFile != "/var/lib/opsgate/oauth.json" || !k.Approval.Web ||
		!k.Providers.Kubernetes.Enabled || k.Providers.Kubernetes.Kubeconfig != "" || k.Server.AuditLog != "" {
		t.Fatalf("kubernetes defaults: %+v", k)
	}
	plain, err := Parse([]byte("server:\n  name: x\n"), "plain")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Auth.Mode != "" || plain.Approval.Web || plain.HTTP.Listen != "127.0.0.1:8787" {
		t.Fatalf("no mode must keep plain defaults: %+v", plain)
	}
}

func TestModeFileWins(t *testing.T) {
	c, err := Parse([]byte("mode: kubernetes\nhttp:\n  listen: 127.0.0.1:9000\napproval:\n  web: false\nauth:\n  builtin:\n    state_file: /data/s.json\n"), "c")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.Listen != "127.0.0.1:9000" || c.Approval.Web || c.Auth.Builtin.StateFile != "/data/s.json" || c.Auth.Builtin.Upstream.Provider != "passkey" {
		t.Fatalf("file values must win: %+v", c)
	}
	// Browser approval follows builtin sign-in instead of failing validation.
	s, err := Parse([]byte("mode: kubernetes\nauth:\n  mode: static\n"), "s")
	if err != nil {
		t.Fatal(err)
	}
	if s.Approval.Web {
		t.Fatal("approval.web defaulted on without builtin sign-in")
	}
}

func TestModeRejects(t *testing.T) {
	for in, want := range map[string]string{
		"mode: pod\n":  "mode must be",
		"mode: host\n": "mode must be",
		"providers:\n  host:\n    enabled: true\n": "field host not found",
		"mode: kubernetes\nunknown: 1\n":           "field unknown not found",
	} {
		if _, err := Parse([]byte(in), "bad"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
	}
	c := Default()
	c.Mode = "pod"
	if err := c.Validate(); err == nil {
		t.Error("Validate accepted an unknown mode")
	}
}

func TestEffectiveRoundTrip(t *testing.T) {
	for _, in := range []string{
		"",
		"mode: kubernetes\napproval:\n  window: 30m\n  window_tools: [\"note_*\"]\n",
		"mode: kubernetes\nproviders:\n  kubernetes:\n    enabled: true\n    clusters:\n      - name: prod\n        namespaces: [apps]\n",
		"watch:\n  webhook_url: https://hooks.example.com/x\n",
	} {
		c, err := Parse([]byte(in), "in")
		if err != nil {
			t.Fatal(err)
		}
		out, err := c.Effective()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "tool_timeout: 30s") {
			t.Fatalf("durations must be readable:\n%s", out)
		}
		again, err := Parse(out, "effective")
		if err != nil {
			t.Fatalf("%q: effective output does not load: %v\n%s", in, err, out)
		}
		out2, err := again.Effective()
		if err != nil || string(out2) != string(out) {
			t.Fatalf("%q: round trip changed:\n%s\n---\n%s", in, out, out2)
		}
	}
}

func TestGenerate(t *testing.T) {
	base := InitOptions{PublicURL: "https://mcp.example.com", Login: LoginPasskey, User: "alice",
		Namespaces: []string{"*"}, DenyNamespaces: []string{"kube-system"}, PodLogs: true, NoteVault: "/notes"}
	for name, o := range map[string]InitOptions{
		"kubernetes": base,
		"github": {PublicURL: "https://ops.example.com", Login: LoginGitHub, User: "octo", ClientID: "id",
			ClientSecretFile: "/etc/opsgate/secret", Write: true, Namespaces: []string{"apps"}},
		"google": {PublicURL: "https://ops.example.com", Login: LoginGoogle, User: "a@example.com", ClientID: "id", ClientSecretFile: "/s"},
	} {
		data, err := Generate(o)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c, err := Parse(data, name)
		if err != nil {
			t.Fatal(err)
		}
		if !c.Approval.RequireWeb || strings.Contains(string(data), "require_web:") {
			t.Fatalf("generated config must inherit web-only approval: %s", data)
		}
		u := c.Auth.Builtin.Users
		if c.Mode != ModeKubernetes || len(u) != 1 || u[0].Subject != o.Subject() || len(c.Auth.Builtin.Admins) != 1 || (len(u[0].Scopes) == 2) != o.Write {
			t.Fatalf("%s: %+v\n%s", name, c.Auth.Builtin, data)
		}
		if !c.Providers.Kubernetes.Enabled || c.Providers.Note.Enabled != (o.NoteVault != "") {
			t.Fatalf("%s: providers %+v", name, c.Providers)
		}
	}
	// Answers are quoted, so YAML syntax in them stays data.
	quoted := base
	quoted.NoteVault = "/notes\" # x: [1]"
	if data, err := Generate(quoted); err != nil {
		t.Fatal(err)
	} else if c, err := Parse(data, "quoted"); err != nil || c.Providers.Note.Vault != quoted.NoteVault {
		t.Fatalf("quoted vault: %v %q", err, c.Providers.Note.Vault)
	}
	for name, o := range map[string]InitOptions{
		"space in user":     {PublicURL: "https://m.example.com", Login: LoginPasskey, User: "a b"},
		"http public url":   {PublicURL: "http://m.example.com", Login: LoginPasskey, User: "a"},
		"github without id": {PublicURL: "https://m.example.com", Login: LoginGitHub, User: "a"},
		"unknown login":     {PublicURL: "https://m.example.com", Login: "saml", User: "a"},
	} {
		if _, err := Generate(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRequireWebDefault(t *testing.T) {
	for _, mode := range []string{ModeKubernetes} {
		for _, tc := range []struct {
			name, settings string
			want           bool
		}{
			{name: "unset", want: true},
			{name: "explicit false", settings: "approval:\n  require_web: false\n"},
			{name: "explicit true", settings: "approval:\n  require_web: true\n", want: true},
			{name: "web disabled", settings: "approval:\n  web: false\n"},
			{name: "external", settings: "auth:\n  mode: external\n"},
			{name: "static", settings: "auth:\n  mode: static\n"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				c, err := Parse([]byte("mode: "+mode+"\nhttp:\n  public_url: https://ops.example\n"+tc.settings), "test")
				if err != nil {
					t.Fatal(err)
				}
				if c.Approval.RequireWeb != tc.want {
					t.Fatalf("require_web = %v, want %v", c.Approval.RequireWeb, tc.want)
				}
				if err := c.ValidateHTTP(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	c, err := Parse([]byte("auth:\n  mode: builtin\napproval:\n  web: true\nhttp:\n  public_url: https://ops.example\n"), "explicit web")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Approval.RequireWeb {
		t.Fatal("explicit web without a mode must default to require_web")
	}
	if err := c.ValidateHTTP(); err != nil {
		t.Fatal(err)
	}
}
