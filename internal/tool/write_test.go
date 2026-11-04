package tool

import (
	"context"
	"crypto/sha256"
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

func connectWrite(t *testing.T, env *Env, spec WriteSpec, h WriteHandler[writeTestInput], opts ...*mcp.ClientOptions) *mcp.ClientSession {
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
	var options *mcp.ClientOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, options)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestAddWriteInputRequired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	input := &InputRequired{
		Requests: mcp.InputRequestMap{"approval": &mcp.ElicitParams{
			Mode: "form", Message: "Approve?", RequestedSchema: map[string]any{"type": "object"},
		}},
		State: "opaque-state-must-not-be-logged",
	}
	var applies atomic.Int32
	env := &Env{
		Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024,
		Fallback: &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}},
		Confirm: confirmFunc(func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error) {
			return "", fmt.Errorf("wrapped: %w", input)
		}),
	}
	cs := connectWrite(t, env, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
			return Plan{Summary: "restart service"}, nil
		},
		Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) {
			applies.Add(1)
			return "changed", nil
		},
	}, &mcp.ClientOptions{MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}})
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "change", Arguments: map[string]any{"target": "service", "password": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsInput() || result.IsError || len(result.Content) != 0 || result.StructuredContent != nil ||
		result.RequestState != input.State || len(result.InputRequests) != 1 || applies.Load() != 0 {
		t.Fatalf("result = %+v; applies = %d", result, applies.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev audit.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Outcome != "pending" || ev.Approval != "elicitation" || ev.Error != "" || ev.OutputSize != 0 ||
		strings.Contains(string(data), input.State) || strings.Contains(input.Error(), input.State) {
		t.Errorf("pending audit = %s", data)
	}
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
					if r.Tool != "change" || r.Caller.User != "operator" || r.Summary != "restart target" || r.ApprovalID != "test-approval" {
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
					return Plan{Summary: "restart target"}, tc.planErr
				},
				Apply: func(ctx context.Context, c Caller, in writeTestInput, p Plan) (string, error) {
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
			if tc.wantPlan > 0 && event.Plan != "restart target" {
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
		{name: "trailing JSON", in: json.RawMessage(`{} {"n":1}`), deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, _, err := writeArgs(tc.in, maxArgumentBytes)
			if errors.Is(err, ErrDenied) != tc.deny {
				t.Fatalf("error = %v", err)
			}
			if !tc.deny && args["n"] != json.Number("9007199254740993") {
				t.Errorf("integer lost precision: %v", args)
			}
		})
	}
}

func TestAddWriteRejectsAmbiguousTarget(t *testing.T) {
	for _, target := range []string{"glpat-aaaaaaaa", "glpat-bbbbbbbb"} {
		t.Run(target, func(t *testing.T) {
			var confirms, applies atomic.Int32
			env := &Env{Timeout: time.Second, MaxOutputBytes: 1024,
				Fallback: &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}},
				Confirm: confirmFunc(func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error) {
					confirms.Add(1)
					return "elicitation", nil
				}),
			}
			cs := connectWrite(t, env, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
				Plan: func(_ context.Context, _ Caller, in writeTestInput) (Plan, error) {
					return Plan{Summary: "Restart deployment prod/" + in.Target}, nil
				},
				Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) {
					applies.Add(1)
					return "changed", nil
				},
			})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: map[string]any{"target": target, "password": ""}})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "target cannot be shown unambiguously") || confirms.Load() != 0 || applies.Load() != 0 {
				t.Fatalf("result = %+v; confirms %d applies %d", result, confirms.Load(), applies.Load())
			}
		})
	}
}

func TestAddWriteFullReviewMaskedArgs(t *testing.T) {
	for _, tc := range []struct {
		name, target, password string
		fullReview, denied     bool
	}{
		{"hidden assignment", strings.Repeat("innocent runbook text\n", 600) + `password="$(rm -rf /srv/data)"`, redact.Mask, true, true},
		{"sensitive key", "service", "hidden", true, true},
		{"complete arguments", strings.Repeat("innocent runbook text\n", 600), redact.Mask, true, false},
		{"short masked arguments", "service", "hidden", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var confirms, applies atomic.Int32
			env := &Env{Timeout: time.Second, MaxOutputBytes: 1024,
				Fallback: &Caller{User: "operator", Transport: "http", Scopes: []string{config.ScopeWrite}},
				Confirm: confirmFunc(func(_ context.Context, _ *mcp.CallToolRequest, r ConfirmRequest) (string, error) {
					confirms.Add(1)
					if r.FullReviewRequired != tc.fullReview {
						t.Error("full review requirement changed")
					}
					return "web", nil
				}),
			}
			cs := connectWrite(t, env, WriteSpec{Name: "change", MaxArgBytes: 64 << 10}, WriteHandler[writeTestInput]{
				Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
					return Plan{Summary: "change service", FullReviewRequired: tc.fullReview}, nil
				},
				Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) {
					applies.Add(1)
					return "changed", nil
				},
			})
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: map[string]any{
				"target": tc.target, "password": tc.password, "approval_id": "glpat-aaaaaaaa",
			}})
			if err != nil {
				t.Fatal(err)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			if result.IsError != tc.denied {
				t.Fatalf("result = %+v: %s", result, text)
			}
			if tc.denied {
				if confirms.Load() != 0 || applies.Load() != 0 || !strings.Contains(text, "arguments cannot be shown unambiguously") ||
					!strings.Contains(text, "split the change") || !strings.Contains(text, "remove the masked value") {
					t.Fatalf("denial = %q; confirms %d applies %d", text, confirms.Load(), applies.Load())
				}
			} else if confirms.Load() != 1 || applies.Load() != 1 {
				t.Fatalf("confirms %d applies %d", confirms.Load(), applies.Load())
			}
		})
	}
}

func TestAddWriteRawArgumentLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	var plans atomic.Int32
	cs := connectWrite(t, &Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024,
		Fallback: &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}},
	}, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
			plans.Add(1)
			return Plan{Summary: "change service"}, nil
		},
		Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) { return "changed", nil },
	})
	// Escapes shrink on decoding, so only a raw-size check catches this input.
	raw := json.RawMessage(`{"target":"service","password":"` + strings.Repeat(`\u0061`, 3000) + `"}`)
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ev audit.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "16 KiB") || plans.Load() != 0 || ev.Args != nil || ev.Outcome != "denied" {
		t.Fatalf("result = %+v; plans %d; audit %s", result, plans.Load(), data)
	}
}

func TestAddWriteMaxArgBytes(t *testing.T) {
	var plans atomic.Int32
	h := WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
			plans.Add(1)
			return Plan{Summary: "change service"}, nil
		},
		Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) { return "changed", nil },
	}
	raw := json.RawMessage(`{"target":"` + strings.Repeat("a", 20<<10) + `","password":"x"}`)
	for _, tc := range []struct {
		max     int
		planned bool
	}{{0, false}, {64 << 10, true}, {8 << 10, false}} {
		plans.Store(0)
		logger, err := audit.Open(filepath.Join(t.TempDir(), "audit"))
		if err != nil {
			t.Fatal(err)
		}
		cs := connectWrite(t, &Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024,
			Fallback: &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}},
		}, WriteSpec{Name: "change", MaxArgBytes: tc.max}, h)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: raw})
		if err != nil {
			t.Fatal(err)
		}
		// Plan runs only when the raw arguments fit; Confirm is nil, so Apply never does.
		if got := plans.Load() == 1; got != tc.planned {
			t.Errorf("MaxArgBytes %d: planned = %v, want %v (%s)", tc.max, got, tc.planned, res.Content[0].(*mcp.TextContent).Text)
		}
		logger.Close()
	}
}

func TestWriteArgsUsesToolLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		limit      int
		input      string
		wantDenied bool
	}{
		{"small boundary", 16, `{"value":"aaaa"}`, false},
		{"small over limit", 16, `{"value":"aaaaa"}`, true},
		{"default over limit", maxArgumentBytes, `{"value":"` + strings.Repeat("a", maxArgumentBytes) + `"}`, true},
		{"large document", 256 << 10, `{"value":"` + strings.Repeat("a", 200<<10) + `"}`, false},
		{"redaction expands beyond limit", 16, `{"password":""}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, hash, err := writeArgs(json.RawMessage(tc.input), tc.limit)
			if errors.Is(err, ErrDenied) != tc.wantDenied {
				t.Fatalf("error = %v", err)
			}
			if tc.wantDenied {
				if args != nil || hash != [sha256.Size]byte{} {
					t.Fatal("denied arguments retained in audit or approval")
				}
			} else {
				encoded, err := json.Marshal(args)
				if err != nil || len(encoded) > tc.limit {
					t.Fatalf("audit argument size = %d, error = %v", len(encoded), err)
				}
			}
		})
	}
}

func TestAddWriteBoundsAuditSummary(t *testing.T) {
	for name, summary := range map[string]string{
		"oversized":         strings.Repeat("x", 64<<10+1),
		"redaction expands": strings.Repeat("password=a ", 4000),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			var confirms atomic.Int32
			cs := connectWrite(t, &Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024,
				Fallback: &Caller{User: "alice", Transport: "stdio", Scopes: config.Scopes},
				Confirm: confirmFunc(func(context.Context, *mcp.CallToolRequest, ConfirmRequest) (string, error) {
					confirms.Add(1)
					return "test", nil
				}),
			}, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
				Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
					return Plan{Summary: summary}, nil
				},
				Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) {
					t.Error("applied oversized summary")
					return "", nil
				},
			})
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "change", Arguments: map[string]any{"target": "service", "password": ""}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var ev audit.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				t.Fatal(err)
			}
			if !res.IsError || confirms.Load() != 0 || len(ev.Plan) > 64<<10 || strings.Contains(ev.Plan, "password=a") || ev.Outcome != "denied" || len(ev.Error) > 1024 {
				t.Fatalf("result = %+v, confirmations = %d, audit size = %d", res, confirms.Load(), len(data))
			}
		})
	}
}

func TestWriteArgsHash(t *testing.T) {
	inputs := []string{
		`{"target":"glpat-aaaaaaaa","n":9007199254740993,"nested":{"b":2,"a":1}}`,
		`{ "nested": {"a":1,"b":2}, "n":9007199254740993,"target":"glpat-aaaaaaaa","approval_id":"ignored" }`,
		`{"target":"glpat-bbbbbbbb","n":9007199254740993,"nested":{"b":2,"a":1}}`,
		`{"target":"glpat-aaaaaaaa","n":9007199254740992,"nested":{"b":2,"a":1}}`,
	}
	var first [32]byte
	for i, input := range inputs {
		args, hash, err := writeArgs(json.RawMessage(input), maxArgumentBytes)
		if err != nil || args["target"] != redact.Mask {
			t.Fatalf("args = %+v; err %v", args, err)
		}
		if i == 0 {
			first = hash
		} else if (hash == first) != (i == 1) {
			t.Errorf("input %d: hash equality = %v", i, hash == first)
		}
	}
}

func TestAddWritePassesInvocationPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	firstConfirm, release := make(chan struct{}), make(chan struct{})
	var plans, confirms atomic.Int32
	type privatePlan struct {
		ID     int32
		Secret string
	}
	env := &Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 1024,
		Fallback: &Caller{User: "operator", Transport: "stdio", Scopes: []string{config.ScopeWrite}},
		Confirm: confirmFunc(func(ctx context.Context, _ *mcp.CallToolRequest, _ ConfirmRequest) (string, error) {
			if confirms.Add(1) == 1 {
				close(firstConfirm)
				select {
				case <-release:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "elicitation", nil
		}),
	}
	cs := connectWrite(t, env, WriteSpec{Name: "change"}, WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
			return Plan{Summary: "Change service", Data: privatePlan{plans.Add(1), "private-plan-data"}}, nil
		},
		Apply: func(_ context.Context, _ Caller, _ writeTestInput, p Plan) (string, error) {
			data, ok := p.Data.(privatePlan)
			if !ok {
				return "", errors.New("invocation plan missing")
			}
			return fmt.Sprint(data.ID), nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	params := &mcp.CallToolParams{Name: "change", Arguments: map[string]any{"target": "service", "password": ""}}
	first := make(chan *mcp.CallToolResult, 1)
	callErr := make(chan error, 1)
	go func() {
		result, err := cs.CallTool(ctx, params)
		callErr <- err
		first <- result
	}()
	select {
	case <-firstConfirm:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second, err := cs.CallTool(ctx, params)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-callErr; err != nil {
		t.Fatal(err)
	}
	for i, result := range []*mcp.CallToolResult{<-first, second} {
		if result.IsError || result.Content[0].(*mcp.TextContent).Text != fmt.Sprint(i+1) {
			t.Fatalf("invocation %d got %+v", i+1, result)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(Plan{Summary: "preview", Data: make(chan int)})
	if err != nil || strings.Contains(string(encoded), "Data") || strings.Contains(string(data), "private-plan-data") {
		t.Fatalf("private plan serialized: %s; audit %s; err %v", encoded, data, err)
	}
}
