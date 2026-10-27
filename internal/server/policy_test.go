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
	all := []string{"host_logs", "host_overview", "host_service_restart", "host_service_status", "host_services", "host_top_processes"}
	for _, tc := range []struct {
		name        string
		allow, deny []string
		want        []string
	}{
		{name: "default", want: all},
		{name: "allow", allow: []string{"host_service*"}, want: []string{"host_service_restart", "host_service_status", "host_services"}},
		{name: "deny wins", allow: []string{"host_*"}, deny: []string{"host_service*"}, want: []string{"host_logs", "host_overview", "host_top_processes"}},
		{name: "deny everything", deny: []string{"*"}, want: []string{}},
		{name: "deny still matches outside allow", allow: []string{"host_overview"}, deny: []string{"host_service_restart"}, want: []string{"host_overview"}},
		{name: "write only", allow: []string{"host_service_restart"}, want: []string{"host_service_restart"}},
		{name: "character classes", allow: []string{"host_[lo]*", "host_?op_processes"}, want: []string{"host_logs", "host_overview", "host_top_processes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Host.Enabled = true
			cfg.Providers.Host.RestartUnits = []string{"example.service"}
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
		allow, host, write  bool
	}{
		{"deny typo", "host_overveiw", `tools.deny pattern "host_overveiw" matches no tool`, false, true, false},
		{"allow typo", "host_overveiw", `tools.allow pattern "host_overveiw" matches no tool`, true, true, false},
		{"disabled provider", "host_*", `tools.deny pattern "host_*" matches no tool`, false, false, false},
		{"disabled write", "host_service_restart", `tools.deny pattern "host_service_restart" matches no tool`, false, true, false},
		{"enabled write", "host_service_restart", "", false, true, true},
		{"malformed", "[", "invalid glob", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Host.Enabled = tc.host
			if tc.write {
				cfg.Providers.Host.RestartUnits = []string{"example.service"}
			}
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
	cfg.Providers.Host.Enabled = true
	cfg.Server.Tools.Deny = []string{"host_*"}
	env := &tool.Env{}
	if _, err := New(cfg, env); err != nil {
		t.Fatal(err)
	}
	cfg.Providers.Host.Enabled = false
	if _, err := New(cfg, env); err == nil {
		t.Fatal("second server inherited the first server's offered tools")
	}
}

func TestToolGlobConfigValidation(t *testing.T) {
	for _, location := range []string{"allow", "deny", "builtin", "external", "static"} {
		for _, pattern := range []string{"host_*", "note_?ead", "host_[a-z]*", "[", "host_\\"} {
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
				valid := pattern != "[" && pattern != "host_\\"
				for _, validate := range []func() error{cfg.Validate, cfg.ValidateHTTP} {
					if err := validate(); (err == nil) != valid {
						t.Fatalf("validation = %v", err)
					}
				}
			})
		}
	}
}
