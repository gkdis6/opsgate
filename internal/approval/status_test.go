package approval

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestStatusOwnershipStatesAndPolicy(t *testing.T) {
	m, mux := newWebManager(t)
	r := approvalRequest()
	r.Target = "prod"
	id := pendingID(t, m, r)
	caller := tool.Caller{User: r.Caller.User, Transport: "http", Scopes: []string{config.ScopeRead}}
	env := &tool.Env{Fallback: &caller, Timeout: time.Second, MaxOutputBytes: 4096}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	m.RegisterStatus(s, env)
	cs := connectApproval(t, s, nil)
	call := func(id string) (bool, string) {
		t.Helper()
		result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "approval_status", Arguments: map[string]any{"approval_id": id}})
		if err != nil {
			t.Fatal(err)
		}
		return result.IsError, result.Content[0].(*mcp.TextContent).Text
	}
	check := func(status string) {
		t.Helper()
		failed, text := call(id)
		if failed || !strings.Contains(text, `"status":"`+status+`"`) || !strings.Contains(text, `"tool":"restart"`) || !strings.Contains(text, `"target":"prod"`) {
			t.Fatalf("status: %s", text)
		}
	}
	check("pending")
	caller.User = "other"
	otherError, other := call(id)
	unknownError, unknown := call(strings.Repeat("a", 26))
	if !otherError || !unknownError || other != unknown || strings.Contains(other, "prod") {
		t.Fatalf("ownership leak: %q %q", other, unknown)
	}
	caller.User = r.Caller.User
	caller.Scopes = nil
	if failed, text := call(id); !failed || !strings.Contains(text, "missing scope ops:read") {
		t.Fatal(text)
	}
	caller.Scopes = []string{config.ScopeRead}
	caller.Tools = []string{"restart"}
	if failed, text := call(id); !failed || !strings.Contains(text, "tool not allowed") {
		t.Fatal(text)
	}
	caller.Tools = nil
	// Exercise running and failure states with the real browser execution.
	started, release, postDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.mu.Lock()
	m.approvals[id].run = func(context.Context) (string, error) {
		close(started)
		<-release
		return "", errors.New("failed password=secret")
	}
	m.mu.Unlock()
	go func() { decide(t, mux, id, "approve"); close(postDone) }()
	<-started
	check("running")
	close(release)
	<-postDone
	check("not_applied")
	if _, text := call(id); !strings.Contains(text, "Not applied:") || strings.Contains(text, "secret") {
		t.Fatal(text)
	}
	id = pendingID(t, m, r)
	decide(t, mux, id, "approve")
	check("applied")
	if _, text := call(id); !strings.Contains(text, "restarted") {
		t.Fatal(text)
	}
	id = pendingID(t, m, r)
	decide(t, mux, id, "reject")
	check("rejected")
	m.now = func() time.Time { return time.Now().Add(time.Hour) }
	if failed, text := call(id); !failed || !strings.Contains(text, "unknown or expired") {
		t.Fatal(text)
	}
}

func TestStatusRegistration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, denied := range []bool{false, true} {
			var browser BrowserAuth
			if enabled {
				browser = fakeBrowser{}
			}
			m := New(config.Approval{TTL: time.Minute}, "", browser, nil)
			policy := config.ToolPolicy{}
			if denied {
				policy.Deny = []string{"approval_status"}
			}
			env := &tool.Env{Policy: tool.NewPolicy(policy)}
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			m.RegisterStatus(s, env)
			cs := connectApproval(t, s, nil)
			list, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if enabled && !denied {
				expected = 1
			}
			if len(list.Tools) != expected {
				t.Fatalf("enabled %v denied %v: %+v", enabled, denied, list.Tools)
			}
			if expected == 1 && (!list.Tools[0].Annotations.ReadOnlyHint || list.Tools[0].Name != "approval_status") {
				t.Fatalf("annotations: %+v", list.Tools[0])
			}
		}
	}
}
