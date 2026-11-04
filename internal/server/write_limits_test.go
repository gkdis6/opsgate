package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type confirmerFunc func(context.Context, *mcp.CallToolRequest, tool.ConfirmRequest) (string, error)

func (f confirmerFunc) Confirm(ctx context.Context, req *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	return f(ctx, req, r)
}

func TestLargeNoteThroughMCP(t *testing.T) {
	for _, realApproval := range []bool{false, true} {
		t.Run(map[bool]string{false: "approving confirmer", true: "approval manager"}[realApproval], func(t *testing.T) {
			cfg := config.Default()
			cfg.Providers.Note.Enabled = true
			cfg.Providers.Note.AllowWrite = true
			cfg.Providers.Note.Vault = t.TempDir()
			auditPath := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(auditPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			content := strings.Repeat("a", 200<<10)
			var maxArgs atomic.Int64
			var prompts atomic.Int32
			manager := approval.New(cfg.Approval, "", nil, logger)
			env := &tool.Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 4096,
				Fallback: &tool.Caller{User: "local", Transport: "stdio", Scopes: config.Scopes},
				Confirm: confirmerFunc(func(ctx context.Context, req *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
					maxArgs.Store(int64(r.MaxArgBytes))
					if r.Args["content"] != content || r.Tool != "note_create" || !strings.Contains(r.Summary, "create note large.md") {
						t.Error("approval did not receive the complete note arguments and plan")
					}
					if realApproval {
						return manager.Confirm(ctx, req, r)
					}
					return "test", nil
				}),
			}
			s, err := New(cfg, env)
			if err != nil {
				t.Fatal(err)
			}
			cs := connect(t, s, &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				prompts.Add(1)
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
			}})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "note_create", Arguments: map[string]any{"path": "large", "content": content}})
			if realApproval {
				// The preview omits content and stdio has no web channel, so a
				// human could not review this write through elicitation.
				if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "split it into smaller writes") {
					t.Fatalf("create = %+v, error = %v", result, err)
				}
				if _, err := os.Stat(filepath.Join(cfg.Providers.Note.Vault, "large.md")); !os.IsNotExist(err) || prompts.Load() != 0 {
					t.Fatalf("stat = %v, prompts = %d", err, prompts.Load())
				}
				return
			}
			if err != nil || result.IsError {
				t.Fatalf("create = %+v, error = %v", result, err)
			}
			data, err := os.ReadFile(filepath.Join(cfg.Providers.Note.Vault, "large.md"))
			if err != nil || string(data) != content {
				t.Fatalf("note size = %d, error = %v", len(data), err)
			}
			if maxArgs.Load() <= int64(len(content)) {
				t.Fatalf("limit = %d", maxArgs.Load())
			}
			f, err := os.Open(auditPath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			dec := json.NewDecoder(f)
			var completed bool
			for {
				var ev audit.Event
				if err := dec.Decode(&ev); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				args, err := json.Marshal(ev.Args)
				if err != nil || len(args) > int(maxArgs.Load()) || ev.Args["content"] != content {
					t.Fatalf("audit arguments size = %d, error = %v", len(args), err)
				}
				completed = completed || ev.Outcome == "ok"
			}
			if !completed {
				t.Fatal("missing successful audit event")
			}
		})
	}
}

type unusedBrowser struct{}

func (unusedBrowser) RequireUser(func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.NotFoundHandler()
}

func TestApprovalUsesToolSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		limit, argsSize, summaryLen int
		valid                       bool
	}{
		{"default argument boundary", 0, 16 << 10, 1, true},
		{"default argument over limit", 0, 16<<10 + 1, 1, false},
		{"smaller tool argument boundary", 1024, 1024, 1, true},
		{"smaller tool argument over limit", 1024, 1025, 1, false},
		{"large arguments", 256 << 10, 200 << 10, 1, true},
		{"large argument boundary", 256 << 10, 256 << 10, 1, true},
		{"large argument over limit", 256 << 10, 256<<10 + 1, 1, false},
		{"default summary boundary", 0, 16, 64 << 10, true},
		{"default summary over limit", 0, 16, 64<<10 + 1, false},
		{"small tool retains summary floor", 1024, 16, 64 << 10, true},
		{"large summary boundary", 256 << 10, 16, 272 << 10, true},
		{"large summary over limit", 256 << 10, 16, 272<<10 + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := approval.New(config.Approval{TTL: time.Minute}, "https://ops.example", unusedBrowser{}, nil)
			args := map[string]any{"value": ""}
			empty, _ := json.Marshal(args)
			args["value"] = strings.Repeat("x", tc.argsSize-len(empty))
			_, err := m.Confirm(context.Background(), nil, tool.ConfirmRequest{
				Caller: tool.Caller{User: "alice", Transport: "http"}, Tool: "change", Args: args,
				Summary: strings.Repeat("x", tc.summaryLen), MaxArgBytes: tc.limit,
			})
			if tc.valid && !errors.Is(err, tool.ErrApprovalPending) || !tc.valid && !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("approval error = %v", err)
			}
		})
	}
}
