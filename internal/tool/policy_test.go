package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
)

func TestPerIdentityTools(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tools   []string
		scopes  []string
		allowed bool
	}{
		{"unrestricted", nil, config.Scopes, true},
		{"empty", []string{}, config.Scopes, true},
		{"glob match", []string{"other", "ch?ng[e]"}, config.Scopes, true},
		{"wildcard", []string{"*"}, config.Scopes, true},
		{"different tool", []string{"read_*"}, config.Scopes, false},
		{"malformed glob", []string{"["}, config.Scopes, false},
		{"tools checked before scopes", []string{"other"}, nil, false},
	} {
		for _, write := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/read", true: "/write"}[write], func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "audit")
				logger, err := audit.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { logger.Close() })
				env := &Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024}
				var handlers, confirms atomic.Int32
				env.Confirm = confirmFunc(func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error) {
					confirms.Add(1)
					return "test", nil
				})
				s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
				s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
					return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
						if call, ok := req.(*mcp.CallToolRequest); ok {
							call.Extra = &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{
								UserID: "alice", Scopes: tc.scopes, Extra: map[string]any{"tools": tc.tools},
							}}
						}
						return next(ctx, method, req)
					}
				})
				checkCaller := func(c Caller) {
					if c.User != "alice" || c.Transport != "http" || !slices.Equal(c.Tools, tc.tools) {
						t.Errorf("caller = %+v", c)
					}
				}
				if write {
					AddWrite(s, env, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
						Plan: func(_ context.Context, c Caller, _ writeTestInput) (Plan, error) {
							handlers.Add(1)
							checkCaller(c)
							return Plan{Summary: "change target"}, nil
						},
						Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) { return "changed", nil },
					})
				} else {
					Add(s, env, Spec{Name: "change", ReadOnly: true}, func(_ context.Context, c Caller, _ testInput) (string, error) {
						handlers.Add(1)
						checkCaller(c)
						return "read", nil
					})
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				t.Cleanup(cancel)
				st, ct := mcp.NewInMemoryTransports()
				ss, err := s.Connect(ctx, st, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { ss.Close() })
				cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { cs.Close() })
				args := map[string]any{"message": "", "password": ""}
				if write {
					args = map[string]any{"target": "service", "password": ""}
				}
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "change", Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				text := result.Content[0].(*mcp.TextContent).Text
				if result.IsError == tc.allowed || (handlers.Load() == 1) != tc.allowed || !tc.allowed && !strings.Contains(text, "tool not allowed for this user") {
					t.Fatalf("result = %+v, text = %s, handlers = %d", result, text, handlers.Load())
				}
				if (confirms.Load() == 1) != (tc.allowed && write) {
					t.Fatalf("confirmations = %d", confirms.Load())
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var ev audit.Event
				if err := json.Unmarshal(data, &ev); err != nil {
					t.Fatal(err)
				}
				if ev.User != "alice" || ev.Transport != "http" || !tc.allowed && ev.Outcome != "denied" {
					t.Fatalf("audit = %+v", ev)
				}
			})
		}
	}
}

func TestCallerToolMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		valid bool
	}{
		{"absent", nil, true},
		{"empty", map[string]any{"tools": []string(nil)}, true},
		{"globs", map[string]any{"tools": []string{"k8s_*"}}, true},
		{"string", map[string]any{"tools": "k8s_*"}, false},
		{"wrong slice type", map[string]any{"tools": []any{"k8s_*"}}, false},
		{"null", map[string]any{"tools": nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &Env{Fallback: &Caller{User: "local"}}
			c, ok := env.caller(&mcp.CallToolRequest{Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{UserID: "alice", Extra: tc.extra}}})
			if ok != tc.valid || ok && c.User != "alice" {
				t.Fatalf("caller = %+v, valid = %v", c, ok)
			}
			if ok && len(c.Tools) > 0 {
				c.Tools[0] = "*"
				if tc.extra["tools"].([]string)[0] != "k8s_*" {
					t.Fatal("caller can mutate verifier tool restrictions")
				}
			}
		})
	}
}
