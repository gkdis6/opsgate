package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testInput struct {
	Password string `json:"password"`
	Message  string `json:"message"`
}

func connectTool(t *testing.T, env *Env, spec Spec, handler Handler[testInput]) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Add(server, env, spec, handler)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestAdd(t *testing.T) {
	caller := &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeRead}}
	for _, tc := range []struct {
		name          string
		fallback      *Caller
		scope, output string
		handlerErr    error
		max           int
		timeout       bool
		outcome       string
		wantError     bool
		wantText      string
	}{
		{name: "missing scope", fallback: caller, scope: config.ScopeWrite, outcome: "denied", wantError: true, wantText: "missing scope"},
		{name: "unauthenticated", outcome: "denied", wantError: true, wantText: "unauthenticated"},
		{name: "handler error", fallback: caller, handlerErr: errors.New("failure password=hidden"), outcome: "error", wantError: true, wantText: "failure password=" + redact.Mask},
		{name: "handler policy refusal", fallback: caller, handlerErr: Denied("unit forbidden"), outcome: "denied", wantError: true, wantText: "unit forbidden"},
		{name: "redaction", fallback: caller, output: "password=hidden", outcome: "ok", wantText: "password=" + redact.Mask},
		{name: "UTF8 truncation", fallback: caller, output: strings.Repeat("界", 100), max: 32, outcome: "ok"},
		{name: "small limit", fallback: caller, output: strings.Repeat("界", 100), max: 5, outcome: "ok"},
		{name: "error limit", fallback: caller, handlerErr: errors.New(strings.Repeat("界", 100)), max: 32, outcome: "error", wantError: true},
		{name: "timeout", fallback: caller, timeout: true, outcome: "error", wantError: true, wantText: "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			max := tc.max
			if max == 0 {
				max = 1024
			}
			env := &Env{Audit: logger, Fallback: tc.fallback, MaxOutputBytes: max, Timeout: 20 * time.Millisecond}
			called := false
			cs := connectTool(t, env, Spec{Name: "check", ReadOnly: true, Scope: tc.scope}, func(ctx context.Context, c Caller, in testInput) (string, error) {
				called = true
				if c.User != "operator" || in.Password != "input-secret" {
					t.Errorf("handler input: %+v %+v", c, in)
				}
				if tc.timeout {
					<-ctx.Done()
					return "", ctx.Err()
				}
				return tc.output, tc.handlerErr
			})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: map[string]any{"password": "input-secret", "message": "token=argument-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError != tc.wantError {
				t.Fatalf("IsError = %v", result.IsError)
			}
			if len(result.Content) != 1 {
				t.Fatalf("content = %+v", result.Content)
			}
			text, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("content type = %T", result.Content[0])
			}
			if !utf8.ValidString(text.Text) || len(text.Text) > max {
				t.Errorf("invalid or oversized output: %q (%d > %d)", text.Text, len(text.Text), max)
			}
			if tc.wantText != "" && !strings.Contains(text.Text, tc.wantText) {
				t.Errorf("text = %q, want %q", text.Text, tc.wantText)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var event audit.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Outcome != tc.outcome || event.Tool != "check" {
				t.Errorf("event = %+v", event)
			}
			if event.Args["password"] != redact.Mask || event.Args["message"] != "token="+redact.Mask {
				t.Errorf("args leaked: %+v", event.Args)
			}
			if strings.Contains(event.Error, "hidden") {
				t.Errorf("error leaked: %q", event.Error)
			}
			if tc.fallback != nil && (event.User != "operator" || event.Transport != "stdio") {
				t.Errorf("identity = %+v", event)
			}
			if tc.outcome == "ok" && event.OutputSize != len(text.Text) {
				t.Errorf("output bytes = %d", event.OutputSize)
			}
			if (tc.name == "missing scope" || tc.name == "unauthenticated") && called {
				t.Error("unauthorized handler invoked")
			}
		})
	}
}

func TestAnnotations(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		t.Run(map[bool]string{true: "read", false: "write"}[readOnly], func(t *testing.T) {
			cs := connectTool(t, &Env{}, Spec{Name: "check", Title: "Check", ReadOnly: readOnly}, func(context.Context, Caller, testInput) (string, error) { return "", nil })
			result, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Tools) != 1 {
				t.Fatalf("tools = %+v", result.Tools)
			}
			a := result.Tools[0].Annotations
			if a == nil || a.ReadOnlyHint != readOnly || a.DestructiveHint == nil || *a.DestructiveHint == readOnly || a.Title != "Check" {
				t.Errorf("annotations = %+v", a)
			}
		})
	}
}

func TestAddRedactsBeforeTruncation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "error"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			const max = 80
			output := "password=" + strings.Repeat("x", 1<<20) + " outside-budget"
			cs := connectTool(t, &Env{Audit: logger, MaxOutputBytes: max, Timeout: time.Second, Fallback: &Caller{Scopes: []string{config.ScopeRead}}}, Spec{Name: "check", ReadOnly: true}, func(context.Context, Caller, testInput) (string, error) {
				if fail {
					return "", errors.New(output)
				}
				return output, nil
			})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: testInput{}})
			if err != nil {
				t.Fatal(err)
			}
			got := result.Content[0].(*mcp.TextContent).Text
			if result.IsError != fail || got != "password="+redact.Mask+" outside-budget" || len(got) > max {
				t.Fatalf("result = %q, error = %v", got, result.IsError)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var ev audit.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				t.Fatal(err)
			}
			if fail && ev.Error != got {
				t.Fatalf("audit error = %q, want %q", ev.Error, got)
			}
		})
	}
}

func TestAddRawArgumentsLimit(t *testing.T) {
	for _, size := range []int{16 << 10, (16 << 10) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			called := false
			cs := connectTool(t, &Env{Audit: logger, MaxOutputBytes: 1024, Timeout: time.Second, Fallback: &Caller{Scopes: []string{config.ScopeRead}}}, Spec{Name: "check", ReadOnly: true}, func(context.Context, Caller, testInput) (string, error) {
				called = true
				return "ok", nil
			})
			// Escapes count toward the raw limit, while the decoded input is
			// much smaller. Checking argsOf(in)'s size would miss the attack.
			padding := size - len(`{"password":"hidden","message":""}`)
			raw := json.RawMessage(`{"password":"hidden","message":"` + strings.Repeat(`\u0061`, padding/6) + strings.Repeat("a", padding%6) + `"}`)
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: raw})
			if err != nil {
				t.Fatal(err)
			}
			oversized := size > 16<<10
			if result.IsError != oversized || called == oversized {
				t.Fatalf("size=%d: called=%v, result=%+v", size, called, result)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var event audit.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if oversized && (len(event.Args) != 0 || event.Outcome != "error") {
				t.Fatalf("oversized arguments audited: %+v", event)
			}
		})
	}
}
