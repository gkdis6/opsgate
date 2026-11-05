package server

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func connect(t *testing.T, s *mcp.Server, options ...*mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	var opts *mcp.ClientOptions
	if len(options) > 0 {
		opts = options[0]
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestToolRegistrationPolicy(t *testing.T) {
	all := []string{"note_append", "note_create", "note_links", "note_list", "note_read", "note_recent", "note_replace", "note_search", "note_tags", "note_trash"}
	for _, tc := range []struct {
		name        string
		allow, deny []string
		want        []string
	}{
		{name: "default", want: all},
		{name: "allow", allow: []string{"note_re*"}, want: []string{"note_read", "note_recent", "note_replace"}},
		{name: "deny wins", allow: []string{"note_l*", "note_re*"}, deny: []string{"note_re*"}, want: []string{"note_links", "note_list"}},
		{name: "deny everything", deny: []string{"*"}, want: []string{}},
		{name: "deny still matches outside allow", allow: []string{"note_read"}, deny: []string{"note_trash"}, want: []string{"note_read"}},
		{name: "write only", allow: []string{"note_trash"}, want: []string{"note_trash"}},
		{name: "character classes", allow: []string{"note_[lt]*", "note_?ead"}, want: []string{"note_links", "note_list", "note_read", "note_tags", "note_trash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Note.Enabled = true
			cfg.Providers.Note.Vault = t.TempDir()
			cfg.Providers.Note.AllowWrite = true
			cfg.Server.Tools = config.ToolPolicy{Allow: tc.allow, Deny: tc.deny}
			s, err := New(cfg, &tool.Env{})
			if err != nil {
				t.Fatal(err)
			}
			cs := connect(t, s)
			result, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, spec := range result.Tools {
				names = append(names, spec.Name)
			}
			slices.Sort(names)
			if !slices.Equal(names, tc.want) {
				t.Fatalf("tools/list = %v, want %v", names, tc.want)
			}
			for _, name := range all {
				if !slices.Contains(names, name) {
					if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
						t.Fatalf("filtered tool %s remains callable", name)
					}
				}
			}
		})
	}
}

func TestUnknownToolPattern(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, want string
		allow, note, write  bool
	}{
		{"deny typo", "note_raed", `tools.deny pattern "note_raed" matches no tool`, false, true, false},
		{"allow typo", "note_raed", `tools.allow pattern "note_raed" matches no tool`, true, true, false},
		{"disabled provider", "note_*", `tools.deny pattern "note_*" matches no tool`, false, false, false},
		{"disabled write", "note_trash", `tools.deny pattern "note_trash" matches no tool`, false, true, false},
		{"enabled write", "note_trash", "", false, true, true},
		{"malformed", "[", "invalid glob", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Note.Enabled = tc.note
			cfg.Providers.Note.Vault = t.TempDir()
			cfg.Providers.Note.AllowWrite = tc.write
			if tc.allow {
				cfg.Server.Tools.Allow = []string{tc.pattern}
			} else {
				cfg.Server.Tools.Deny = []string{tc.pattern}
			}
			s, err := New(cfg, &tool.Env{})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || s != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("server = %v, error = %v", s, err)
			}
		})
	}
}

func TestPolicyInventoryIsPerServer(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.Note.Enabled = true
	cfg.Providers.Note.Vault = t.TempDir()
	cfg.Server.Tools.Deny = []string{"note_*"}
	env := &tool.Env{}
	if _, err := New(cfg, env); err != nil {
		t.Fatal(err)
	}
	cfg.Providers.Note.Enabled = false
	if _, err := New(cfg, env); err == nil {
		t.Fatal("second server inherited the first server's offered tools")
	}
}

func TestToolGlobConfigValidation(t *testing.T) {
	for _, location := range []string{"allow", "deny", "builtin", "external", "static"} {
		for _, pattern := range []string{"k8s_*", "note_?ead", "k8s_[a-z]*", "[", "k8s_\\"} {
			t.Run(location+"/"+pattern, func(t *testing.T) {
				cfg := config.Default()
				cfg.HTTP.PublicURL, cfg.Auth.Mode = "https://ops.example", config.AuthBuiltin
				switch location {
				case "allow":
					cfg.Server.Tools.Allow = []string{pattern}
				case "deny":
					cfg.Server.Tools.Deny = []string{pattern}
				case "builtin":
					cfg.Auth.Builtin.Users = []config.User{{Subject: "github:alice", Tools: []string{pattern}}}
				case "external":
					cfg.Auth.External.Users = []config.User{{Subject: "claim:alice", Tools: []string{pattern}}}
				case "static":
					cfg.Auth.Static = []config.StaticToken{{Name: "alice", Tools: []string{pattern}}}
				}
				valid := pattern != "[" && pattern != "k8s_\\"
				for _, validate := range []func() error{cfg.Validate, cfg.ValidateHTTP} {
					if err := validate(); (err == nil) != valid {
						t.Fatalf("validation = %v", err)
					}
				}
			})
		}
	}
}
