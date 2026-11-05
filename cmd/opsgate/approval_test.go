package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type approvalBrowser struct{}

func (approvalBrowser) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next(w, r, "passkey:alice") })
}

func TestDefaultApprovalRejectsClientSelfApproval(t *testing.T) {
	for _, mode := range []string{config.ModeKubernetes} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := config.Parse([]byte("mode: "+mode+"\n"), "attack")
			if err != nil {
				t.Fatal(err)
			}
			env := &tool.Env{
				Confirm:  approval.New(cfg.Approval, "https://ops.example", approvalBrowser{}, nil),
				Fallback: &tool.Caller{User: "passkey:alice", Transport: "http", Scopes: []string{config.ScopeWrite}},
				Timeout:  time.Second, MaxOutputBytes: 4096,
			}
			var applied, elicited atomic.Int32
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			tool.AddWrite(s, env, tool.WriteSpec{Name: "write"}, tool.WriteHandler[tool.Approval]{
				Plan: func(context.Context, tool.Caller, tool.Approval) (tool.Plan, error) {
					return tool.Plan{Summary: "Restart service"}, nil
				},
				Apply: func(context.Context, tool.Caller, tool.Approval, tool.Plan) (string, error) {
					applied.Add(1)
					return "restarted", nil
				},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			st, ct := mcp.NewInMemoryTransports()
			ss, err := s.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "malicious", Version: "1"}, &mcp.ClientOptions{
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					elicited.Add(1)
					return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
				},
			})
			cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "write", Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			if applied.Load() != 0 || elicited.Load() != 0 {
				t.Fatalf("client approved its own write: applied=%d elicited=%d", applied.Load(), elicited.Load())
			}
			if result.IsError || result.NeedsInput() || len(result.Content) == 0 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "/approve/") {
				t.Fatalf("expected browser approval link: %+v", result)
			}
		})
	}
}

func TestStdioApprovalServer(t *testing.T) {
	path := os.Getenv("OPSGATE_TEST_STDIO_CONFIG")
	if path == "" {
		return
	}
	if err := run(path, "stdio"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestStdioModeApproval(t *testing.T) {
	for _, mode := range []string{config.ModeKubernetes} {
		for _, setting := range []string{"unset", "false", "true", "shown"} {
			t.Run(mode+"/"+setting, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "opsgate.yaml")
				data := fmt.Sprintf("mode: %s\nserver:\n  audit_log: %q\nstdio:\n  scopes: [ops:read, ops:write]\nhttp:\n  public_url: https://ops.example\nproviders:\n  kubernetes:\n    enabled: false\n  note:\n    enabled: true\n    vault: %q\n    allow_write: true\n", mode, filepath.Join(dir, "audit"), dir)
				if setting == "true" || setting == "false" {
					data += "approval:\n  require_web: " + setting + "\n"
				}
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
				if setting == "shown" {
					c, out, errout, _ := testApp()
					if code := c.execute([]string{"config", "show", "-config", path}); code != 0 {
						t.Fatalf("config show exit=%d: %s", code, errout)
					}
					cfg, err := config.Parse(out.Bytes(), "shown")
					if err != nil {
						t.Fatal(err)
					}
					if !cfg.Approval.RequireWeb || !strings.Contains(out.String(), "require_web: true") {
						t.Fatalf("config show did not print effective web requirement: %s", out)
					}
					if err := cfg.ValidateHTTP(); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioApprovalServer$")
				cmd.Env = append(os.Environ(), "OPSGATE_TEST_STDIO_CONFIG="+path)
				cmd.Stderr = os.Stderr
				var elicited atomic.Int32
				client := mcp.NewClient(&mcp.Implementation{Name: "local", Version: "1"}, &mcp.ClientOptions{
					ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
						elicited.Add(1)
						return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
					},
				})
				cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "note_create", Arguments: map[string]any{"path": "created.md", "content": "hello"}})
				if err != nil {
					t.Fatal(err)
				}
				_, statErr := os.Stat(filepath.Join(dir, "created.md"))
				if setting == "true" || setting == "shown" {
					if !result.IsError || elicited.Load() != 0 || !os.IsNotExist(statErr) {
						t.Fatalf("explicit require_web allowed stdio: result=%+v elicited=%d stat=%v", result, elicited.Load(), statErr)
					}
				} else if result.IsError || elicited.Load() != 1 || statErr != nil {
					t.Fatalf("stdio elicitation failed: result=%+v elicited=%d stat=%v", result, elicited.Load(), statErr)
				}
			})
		}
	}
}

func TestHTTPElicitationWarning(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		warn           bool
	}{
		{name: "default"},
		{name: "opt in", settings: "approval:\n  require_web: false\n", warn: true},
		{name: "web disabled", settings: "approval:\n  web: false\n", warn: true},
		{name: "static", settings: "auth:\n  mode: static\n", warn: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Parse([]byte("mode: kubernetes\nhttp:\n  public_url: https://ops.example\n  listen: 127.0.0.1:99999\nproviders:\n  kubernetes:\n    enabled: false\n"+tc.settings), "warning")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Auth.Builtin.StateFile = filepath.Join(t.TempDir(), "oauth.json")
			cfg.Auth.Builtin.Users = []config.User{{Subject: "passkey:alice", Scopes: []string{config.ScopeRead}}}
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			// An invalid port stops startup before opening a socket.
			if err := runHTTP(context.Background(), cfg, &tool.Env{}); err == nil || !strings.Contains(err.Error(), "invalid port") {
				t.Fatalf("expected invalid port: %v", err)
			}
			warning := "warning: a compromised client can approve its own writes through elicitation; approval.require_web: true avoids this where web approval is available"
			want := 0
			if tc.warn {
				want = 1
			}
			if got := strings.Count(logs.String(), warning); got != want {
				t.Fatalf("warning count=%d, want=%d: %s", got, want, &logs)
			}
		})
	}
}
