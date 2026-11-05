package approval

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/provider/dev"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestWindowConfig(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   time.Duration
		patterns []string
		valid    bool
	}{
		{"off", 0, nil, true},
		{"minimum", time.Minute, []string{"note_*", "k8s_?cale"}, true},
		{"maximum", 12 * time.Hour, []string{"dev_task_start"}, true},
		{"negative", -time.Minute, nil, false},
		{"too short", time.Minute - 1, nil, false},
		{"too long", 12*time.Hour + 1, nil, false},
		{"invalid glob while off", 0, []string{"["}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.HTTP.PublicURL, cfg.Auth.Mode = "https://ops.example", config.AuthBuiltin
			cfg.Approval.Window, cfg.Approval.WindowTools = tc.window, tc.patterns
			for _, validate := range []func() error{cfg.Validate, cfg.ValidateHTTP} {
				if err := validate(); (err == nil) != tc.valid {
					t.Fatalf("validation = %v", err)
				}
			}
		})
	}
}

func TestSummaryLimit(t *testing.T) {
	for _, size := range []int{32 << 10, 64 << 10, 64<<10 + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			m, _ := newWebManager(t)
			r := approvalRequest()
			r.Summary = strings.Repeat("x", size)
			_, err := m.Confirm(context.Background(), nil, r)
			if size <= 64<<10 && !errors.Is(err, tool.ErrApprovalPending) || size > 64<<10 && !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("size %d: %v", size, err)
			}
		})
	}
}

// A missing local runner lets us distinguish the argument guard from planning
// without a socket listener, process, or network dependency.
func TestDevStartArgumentLimit(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	env := &tool.Env{Timeout: time.Second, MaxOutputBytes: 4096, Fallback: &tool.Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeDev}}}
	if err := dev.Register(s, env, config.Dev{RunnerSocket: filepath.Join(t.TempDir(), "missing.sock")}); err != nil {
		t.Fatal(err)
	}
	cs := connectApproval(t, s, nil)
	for _, tc := range []struct {
		name, prompt string
		rawSize      int
		want         string
	}{
		{"32 KiB prompt reaches planning", strings.Repeat("x", 32<<10), 0, "runner unavailable"},
		{"48 KiB wire size reaches planning", "fix", 48 << 10, "runner unavailable"},
		{"over 48 KiB denied", "fix", 48<<10 + 1, "write arguments must be JSON of at most 48 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"project": "app", "agent": "fake", "prompt": tc.prompt}
			if tc.rawSize != 0 {
				data, _ := json.Marshal(args)
				args["project"] = "app" + strings.Repeat("x", tc.rawSize-len(data))
			}
			result := approvalCall(t, cs, &mcp.CallToolParams{Name: "dev_task_start", Arguments: args})
			if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, tc.want) {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}
