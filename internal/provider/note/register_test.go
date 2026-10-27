package note

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type confirmerFunc func(tool.ConfirmRequest) error

func (f confirmerFunc) Confirm(_ context.Context, _ *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	return "test", f(r)
}

func TestRegisterAndMCPCalls(t *testing.T) {
	for _, write := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "write"}[write], func(t *testing.T) {
			cfg := config.Default().Providers.Note
			cfg.Vault = t.TempDir()
			cfg.AllowWrite = write
			put(t, cfg.Vault, "note.md", "hello\n")
			al, err := audit.Open(t.TempDir() + "/audit.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer al.Close()
			calls := 0
			env := &tool.Env{Audit: al, Timeout: time.Second, MaxOutputBytes: 65536, Fallback: &tool.Caller{User: "test", Scopes: []string{config.ScopeRead, config.ScopeWrite}}, Confirm: confirmerFunc(func(r tool.ConfirmRequest) error {
				calls++
				if !strings.Contains(r.Summary, "create note new.md") {
					t.Fatal(r.Summary)
				}
				return nil
			})}
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			if err := Register(s, env, cfg); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			st, ct := mcp.NewInMemoryTransports()
			ss, err := s.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			cs, err := c.Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			listed, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 6
			if write {
				want = 10
			}
			if len(listed.Tools) != want {
				t.Fatal(listed.Tools)
			}
			for _, spec := range listed.Tools {
				if !strings.Contains(spec.Description, "untrusted data") {
					t.Fatal(spec.Description)
				}
				isWrite := spec.Name == "note_create" || spec.Name == "note_append" || spec.Name == "note_replace" || spec.Name == "note_trash"
				if spec.Annotations == nil || spec.Annotations.ReadOnlyHint == isWrite {
					t.Fatalf("bad annotation: %+v", spec)
				}
			}
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "note_read", Arguments: map[string]any{"path": "note"}})
			if err != nil || result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "hello") {
				t.Fatalf("%+v %v", result, err)
			}
			if write {
				result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "note_create", Arguments: map[string]any{"path": "new", "content": "created"}})
				if err != nil || result.IsError || calls != 1 {
					t.Fatalf("%+v %v calls=%d", result, err, calls)
				}
				env.Confirm = nil
				result, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "note_append", Arguments: map[string]any{"path": "note", "content": "denied"}})
				if err != nil || !result.IsError {
					t.Fatalf("%+v %v", result, err)
				}
			}
		})
	}
	cfg := config.Default().Providers.Note
	cfg.Vault = t.TempDir() + "/missing"
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	if err := Register(s, &tool.Env{}, cfg); err == nil {
		t.Fatal("missing vault registered")
	}
}
