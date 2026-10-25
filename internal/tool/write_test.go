package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
)

type writeTestInput struct {
	Approval
	Target   string `json:"target"`
	Password string `json:"password"`
}

type confirmFunc func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error)

func (f confirmFunc) Confirm(ctx context.Context, req *mcp.CallToolRequest, r ConfirmRequest) (string, error) {
	return f(ctx, req, r)
}

func connectWrite(t *testing.T, env *Env, spec WriteSpec, h WriteHandler[writeTestInput]) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	AddWrite(s, env, spec, h)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestAddWrite(t *testing.T) {
	writer := &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}}
	reader := &Caller{User: "reader", Transport: "stdio", Scopes: []string{config.ScopeRead}}
	for _, tc := range []struct {
		name, outcome, method, wantText  string
		caller                           *Caller
		planErr, confirmErr, applyErr    error
		nilConfirm, planTimeout          bool
		applyTimeout, slowConfirm        bool
		wantPlan, wantConfirm, wantApply int32
		max                              int
	}{
		{name: "unauthenticated", outcome: "denied", wantText: "unauthenticated"},
		{name: "missing scope", caller: reader, outcome: "denied", wantText: "missing scope"},
		{name: "plan error", caller: writer, planErr: errors.New("invalid password=hidden"), outcome: "error", wantPlan: 1, wantText: "password=" + redact.Mask},
		{name: "plan policy denial", caller: writer, planErr: Denied("forbidden"), outcome: "denied", wantPlan: 1},
		{name: "plan timeout ignored by handler", caller: writer, planTimeout: true, outcome: "error", wantPlan: 1, wantText: "deadline exceeded"},
		{name: "nil confirmer", caller: writer, nilConfirm: true, outcome: "denied", wantPlan: 1, wantText: "no human confirmation"},
		{name: "denied", caller: writer, confirmErr: Denied("rejected by human"), method: "elicitation", outcome: "denied", wantPlan: 1, wantConfirm: 1},
		{name: "pending", caller: writer, confirmErr: fmt.Errorf("ask user: %w", ErrApprovalPending), method: "web", outcome: "pending", wantText: "ask user", wantPlan: 1, wantConfirm: 1},
		{name: "confirmation error", caller: writer, confirmErr: errors.New("confirmation failed"), outcome: "error", wantPlan: 1, wantConfirm: 1},
		{name: "approved", caller: writer, method: "web", outcome: "ok", wantPlan: 1, wantConfirm: 1, wantApply: 1, wantText: "password=" + redact.Mask},
		{name: "slow human", caller: writer, method: "elicitation", slowConfirm: true, outcome: "ok", wantPlan: 1, wantConfirm: 1, wantApply: 1},
		{name: "apply error", caller: writer, method: "web", applyErr: errors.New("apply password=hidden"), outcome: "error", wantPlan: 1, wantConfirm: 1, wantApply: 1},
		{name: "apply policy denial", caller: writer, method: "web", applyErr: Denied("state changed"), outcome: "denied", wantPlan: 1, wantConfirm: 1, wantApply: 1},
		{name: "apply timeout", caller: writer, method: "web", applyTimeout: true, outcome: "error", wantPlan: 1, wantConfirm: 1, wantApply: 1},
		{name: "truncated output", caller: writer, method: "web", outcome: "ok", wantPlan: 1, wantConfirm: 1, wantApply: 1, max: 35},
		{name: "truncated error", caller: writer, method: "web", applyErr: errors.New(strings.Repeat("界", 100)), outcome: "error", wantPlan: 1, wantConfirm: 1, wantApply: 1, max: 35},
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
			var plans, confirms, applies atomic.Int32
			timeout := time.Second
			if tc.planTimeout || tc.applyTimeout {
				timeout = 20 * time.Millisecond
			} else if tc.slowConfirm {
				timeout = 100 * time.Millisecond
			}
			env := &Env{Audit: logger, Fallback: tc.caller, Timeout: timeout, MaxOutputBytes: max}
			if !tc.nilConfirm {
				env.Confirm = confirmFunc(func(ctx context.Context, req *mcp.CallToolRequest, r ConfirmRequest) (string, error) {
					confirms.Add(1)
					if r.Tool != "change" || r.Caller.User != "operator" || r.Summary != "restart target password="+redact.Mask || r.ApprovalID != "test-approval" {
						t.Errorf("confirmation = %+v", r)
					}
					if _, exists := r.Args["approval_id"]; exists || r.Args["password"] != redact.Mask || r.Args["target"] != "service" {
						t.Errorf("confirmation args = %+v", r.Args)
					}
					if tc.slowConfirm {
						select {
						case <-time.After(300 * time.Millisecond):
						case <-ctx.Done():
							t.Error("human confirmation used the tool timeout")
							return "", ctx.Err()
						}
					}
					return tc.method, tc.confirmErr
				})
			}
			cs := connectWrite(t, env, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
				Plan: func(ctx context.Context, c Caller, in writeTestInput) (Plan, error) {
					plans.Add(1)
					if c.User != "operator" || in.Target != "service" || in.Password != "input-secret" {
						t.Errorf("plan input = %+v %+v", c, in)
					}
					if tc.planTimeout {
						<-ctx.Done()
					}
					return Plan{Summary: "restart target password=hidden"}, tc.planErr
				},
				Apply: func(ctx context.Context, c Caller, in writeTestInput) (string, error) {
					applies.Add(1)
					if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil {
						t.Error("apply must receive a fresh bounded context")
					}
					if tc.applyTimeout {
						<-ctx.Done()
						return "", ctx.Err()
					}
					return "password=hidden " + strings.Repeat("界", 100), tc.applyErr
				},
			})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: map[string]any{
				"target": "service", "password": "input-secret", "approval_id": "test-approval",
			}})
			if err != nil {
				t.Fatal(err)
			}
			wantError := tc.outcome != "ok" && tc.outcome != "pending"
			if result.IsError != wantError || len(result.Content) != 1 {
				t.Fatalf("result = %+v", result)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			if !strings.Contains(text, tc.wantText) || len(text) > max || !utf8.ValidString(text) || strings.Contains(text, "hidden") {
				t.Errorf("result text = %q", text)
			}
			if plans.Load() != tc.wantPlan || confirms.Load() != tc.wantConfirm || applies.Load() != tc.wantApply {
				t.Errorf("calls = plan %d confirm %d apply %d", plans.Load(), confirms.Load(), applies.Load())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var event audit.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Outcome != tc.outcome || event.Approval != tc.method || event.Tool != "change" {
				t.Errorf("audit = %+v", event)
			}
			if tc.wantPlan > 0 && event.Plan != "restart target password="+redact.Mask {
				t.Errorf("audit plan = %q", event.Plan)
			}
			if _, ok := event.Args["approval_id"]; ok || event.Args["password"] != redact.Mask || strings.Contains(string(data), "hidden") || strings.Contains(string(data), "input-secret") {
				t.Errorf("audit leaked arguments: %s", data)
			}
			if tc.caller != nil && (event.User != tc.caller.User || event.Transport != tc.caller.Transport) {
				t.Errorf("audit caller = %+v", event)
			}
			if !wantError && event.OutputSize != len(text) {
				t.Errorf("audit output size = %d, want %d", event.OutputSize, len(text))
			}
		})
	}
}

func TestWriteAnnotations(t *testing.T) {
	for _, idempotent := range []bool{true, false} {
		t.Run(fmt.Sprint(idempotent), func(t *testing.T) {
			cs := connectWrite(t, &Env{}, WriteSpec{Name: "change", Title: "Change", Idempotent: idempotent}, WriteHandler[writeTestInput]{})
			result, err := cs.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			a := result.Tools[0].Annotations
			if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint || a.IdempotentHint != idempotent || a.Title != "Change" {
				t.Errorf("annotations = %+v", a)
			}
		})
	}
}

func TestWriteArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		deny bool
	}{
		{name: "large integer", in: struct {
			N uint64 `json:"n"`
		}{N: 9007199254740993}},
		{name: "oversized", in: map[string]any{"value": strings.Repeat("a", 64<<10)}, deny: true},
		{name: "unencodable", in: make(chan int), deny: true},
		{name: "not object", in: []int{1}, deny: true},
		{name: "null", in: nil, deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := writeArgs(tc.in)
			if errors.Is(err, ErrDenied) != tc.deny {
				t.Fatalf("error = %v", err)
			}
			if !tc.deny && args["n"] != json.Number("9007199254740993") {
				t.Errorf("integer lost precision: %v", args)
			}
		})
	}
}
