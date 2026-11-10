package approval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func waitForRecord(t *testing.T, m *Manager) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		m.mu.Lock()
		var id string
		for key := range m.approvals {
			id = key
			break
		}
		m.mu.Unlock()
		if id != "" {
			return id
		}
		select {
		case <-deadline:
			t.Fatal("approval was not created")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestWaitingDecision(t *testing.T) {
	for _, decision := range []string{"approve", "approve_window", "reject", "failure", "cancel", "cancel_running", "timeout"} {
		t.Run(decision, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			m.wait = time.Second
			if decision == "timeout" {
				m.wait = 5 * time.Millisecond
			}
			r := approvalRequest()
			started, release := make(chan struct{}), make(chan struct{})
			r.Run = func(ctx context.Context) (string, error) {
				if ctx.Err() != nil {
					t.Errorf("run inherited cancellation: %v", ctx.Err())
				}
				close(started)
				<-release
				if decision == "failure" {
					return "", errors.New("provider failed")
				}
				return "done", nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := m.Confirm(ctx, nil, r); result <- err }()
			id := waitForRecord(t, m)
			if decision == "cancel" || decision == "timeout" {
				if decision == "cancel" {
					cancel()
				}
				err := <-result
				if decision == "cancel" && !errors.Is(err, context.Canceled) || decision == "timeout" && !errors.Is(err, tool.ErrApprovalPending) {
					t.Fatalf("wait = %v", err)
				}
				close(release)
				decide(t, mux, id, "approve")
				if !appliedResult(m.ResolveApproval(context.Background(), withID(r, id))) {
					t.Fatal("approval after waiter left did not run")
				}
				return
			}
			if decision == "reject" {
				decide(t, mux, id, "reject")
				if err := <-result; !errors.Is(err, tool.ErrDenied) {
					t.Fatal(err)
				}
				return
			}
			csrf := readCSRF(t, mux, id)
			postDone := make(chan int, 1)
			choice := "approve"
			if decision == "approve_window" {
				choice = decision
			}
			go func() {
				postDone <- webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {choice}}, nil).Code
			}()
			<-started
			// Waiting for execution holds no manager lock; a second tab cannot run it.
			if w := webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {choice}}, nil); w.Code != http.StatusConflict {
				t.Fatalf("second submit: %d", w.Code)
			}
			select {
			case err := <-result:
				t.Fatalf("returned before execution finished: %v", err)
			default:
			}
			if decision == "cancel_running" {
				cancel()
				if err := <-result; !errors.Is(err, context.Canceled) {
					t.Fatalf("running wait: %v", err)
				}
				close(release)
				if code := <-postDone; code != http.StatusOK {
					t.Fatal(code)
				}
				if !appliedResult(m.ResolveApproval(context.Background(), withID(r, id))) {
					t.Fatal("cancellation stopped approved execution")
				}
				return
			}
			close(release)
			err := <-result
			var stored *tool.ApprovalResult
			if !errors.As(err, &stored) || stored.Failed != (decision == "failure") {
				t.Fatalf("outcome = %v", err)
			}
			if stored.Failed && !strings.Contains(stored.Text, "Not applied: provider failed") {
				t.Fatal(stored.Text)
			}
			if code := <-postDone; code != http.StatusOK {
				t.Fatal(code)
			}
		})
	}
}

func withID(r tool.ConfirmRequest, id string) tool.ConfirmRequest { r.ApprovalID = id; return r }

func TestMCPExecutionRecheckIdentityAuditAndReplay(t *testing.T) {
	for _, change := range []string{"none", "summary", "target", "denied", "apply failure", "timeout"} {
		t.Run(change, func(t *testing.T) {
			m, mux := newWebManager(t)
			logPath := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			m.audit = logger
			caller := approvalRequest().Caller
			caller.Tools = []string{"restart", "approval_status"}
			caller.Scopes = []string{config.ScopeRead, config.ScopeWrite}
			env := &tool.Env{Confirm: m, Audit: logger, Timeout: 100 * time.Millisecond, MaxOutputBytes: 4096, Fallback: &caller}
			var changed atomic.Bool
			var applies atomic.Int32
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			tool.AddWrite(s, env, tool.WriteSpec{Name: "restart"}, tool.WriteHandler[restartInput]{
				Plan: func(ctx context.Context, c tool.Caller, in restartInput) (tool.Plan, error) {
					if c.User != "github:operator" || c.Transport != "http" || !c.Has(config.ScopeWrite) || len(c.Tools) != 2 || in.Unit != "nginx.service" {
						t.Errorf("original caller/input lost: %+v %+v", c, in)
					}
					p := tool.Plan{Summary: "Restart nginx.service", Target: "prod"}
					if changed.Load() {
						switch change {
						case "summary":
							p.Summary += " (replaced)"
						case "target":
							p.Target = "other"
						case "denied":
							return tool.Plan{}, tool.Denied("workload is no longer allowed")
						}
					}
					return p, nil
				},
				Apply: func(ctx context.Context, c tool.Caller, _ restartInput, _ tool.Plan) (string, error) {
					applies.Add(1)
					if _, ok := ctx.Deadline(); !ok {
						t.Error("apply has no deadline")
					}
					if c.User != "github:operator" || !c.Has(config.ScopeWrite) {
						t.Errorf("apply caller: %+v", c)
					}
					if change == "apply failure" {
						return "", errors.New("provider password=secret")
					}
					if change == "timeout" {
						<-ctx.Done()
						return "", ctx.Err()
					}
					return "restarted password=secret", nil
				},
			})
			cs := connectApproval(t, s, nil)
			call := func(id, unit string) *mcp.CallToolResult {
				t.Helper()
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": unit, "approval_id": id}})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			result := call("", "nginx.service")
			id := linkID.FindStringSubmatch(result.Content[0].(*mcp.TextContent).Text)[1]
			// Mutating a reused fallback identity must not alter the stored caller.
			caller.Scopes[1], caller.Tools[0] = "removed", "other"
			changed.Store(true)
			decide(t, mux, id, "approve")
			wantApply := int32(0)
			if change == "none" || change == "apply failure" || change == "timeout" {
				wantApply = 1
			}
			if applies.Load() != wantApply {
				t.Fatalf("applied = %d", applies.Load())
			}
			caller.Scopes[1], caller.Tools[0] = config.ScopeWrite, "restart"
			for range 2 {
				result := call(id, "nginx.service")
				text := result.Content[0].(*mcp.TextContent).Text
				// A timed-out apply may have reached the target, so it must
				// not be reported as not applied.
				prefix := "Not applied:"
				if change == "timeout" {
					prefix = "Check the target: outcome unknown"
				}
				if result.IsError != (change != "none") || strings.Contains(text, "secret") || change != "none" && !strings.Contains(text, prefix) {
					t.Fatalf("retry: %+v %s", result, text)
				}
			}
			if result := call(id, "different.service"); !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "invalid or expired approval") {
				t.Fatalf("changed args: %+v", result)
			}
			if applies.Load() != wantApply {
				t.Fatal("retry reapplied")
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			var applyEvents int
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var event audit.Event
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Approval == "web" && event.Args["unit"] == "nginx.service" && event.Outcome != "pending" {
					applyEvents++
					if event.User != "github:operator" || event.Transport != "http" || event.Outcome == "ok" != (change == "none") {
						t.Fatalf("apply audit: %s", line)
					}
				}
			}
			if applyEvents != 1 || strings.Contains(string(data), "secret") {
				t.Fatalf("apply audits %d: %s", applyEvents, data)
			}
		})
	}
}

func TestExecutionRetention(t *testing.T) {
	for _, ttl := range []time.Duration{time.Minute, time.Hour} {
		m, mux := newWebManager(t)
		now := time.Now()
		m.now = func() time.Time { return now }
		m.ttl = ttl
		r := approvalRequest()
		id := pendingID(t, m, r)
		decide(t, mux, id, "approve")
		duration := max(ttl, 10*time.Minute)
		now = now.Add(duration - time.Nanosecond)
		if err := m.ResolveApproval(context.Background(), withID(r, id)); !appliedResult(err) {
			t.Fatal(err)
		}
		now = now.Add(time.Nanosecond)
		if err := m.ResolveApproval(context.Background(), withID(r, id)); !errors.Is(err, tool.ErrDenied) {
			t.Fatal(err)
		}
	}
}

func TestConcurrentApprovalRunsOnce(t *testing.T) {
	m, mux := newWebManager(t)
	r := approvalRequest()
	var applies atomic.Int32
	r.Run = func(context.Context) (string, error) { applies.Add(1); return "done", nil }
	id := pendingID(t, m, r)
	csrf := readCSRF(t, mux, id)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {"approve"}}, nil)
		})
	}
	wg.Wait()
	if applies.Load() != 1 {
		t.Fatalf("runs = %d", applies.Load())
	}
}

func TestMCPWaitReturnsExecution(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			m, mux := newWebManager(t)
			m.wait = time.Second
			logPath := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			m.audit = logger
			caller := approvalRequest().Caller
			env := &tool.Env{Confirm: m, Audit: logger, Fallback: &caller, Timeout: time.Second, MaxOutputBytes: 4096}
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			var applies atomic.Int32
			tool.AddWrite(s, env, tool.WriteSpec{Name: "restart"}, tool.WriteHandler[restartInput]{
				Plan: func(context.Context, tool.Caller, restartInput) (tool.Plan, error) {
					return tool.Plan{Summary: "Restart service"}, nil
				},
				Apply: func(context.Context, tool.Caller, restartInput, tool.Plan) (string, error) {
					applies.Add(1)
					if fail {
						return "", errors.New("provider failed")
					}
					return "restarted", nil
				},
			})
			cs := connectApproval(t, s, nil)
			finished := make(chan *mcp.CallToolResult, 1)
			callErr := make(chan error, 1)
			go func() {
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "service"}})
				callErr <- err
				finished <- result
			}()
			id := waitForRecord(t, m)
			decide(t, mux, id, "approve")
			result := <-finished
			if err := <-callErr; err != nil {
				t.Fatal(err)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			if result.IsError != fail || !fail && text != "restarted" || fail && text != "Not applied: provider failed" || applies.Load() != 1 {
				t.Fatalf("result: %+v %s; applies %d", result, text, applies.Load())
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 3 {
				t.Fatalf("expected pending, approved, apply: %s", data)
			}
		})
	}
}

func TestExecutionIgnoresBrowserDisconnect(t *testing.T) {
	m, _ := newWebManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	cancel()
	p := &pending{}
	result := m.runDirect(ctx, p, func(ctx context.Context) (string, error) {
		if ctx.Err() != nil {
			t.Fatalf("browser cancellation reached execution: %v", ctx.Err())
		}
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("browser deadline reached execution")
		}
		return "done", nil
	})
	if result != "done" || !p.finished {
		t.Fatalf("outcome = %+v", p)
	}
}

func TestApprovalIDCannotUseWindow(t *testing.T) {
	m, mux := windowManager(windowConfig(), nil)
	r := approvalRequest()
	var runs atomic.Int32
	r.Run = func(context.Context) (string, error) { runs.Add(1); return "done", nil }
	id := pendingID(t, m, r)
	decide(t, mux, id, "approve_window")
	// The same ID reads its outcome, even while a window is active.
	if method, err := m.Confirm(context.Background(), nil, withID(r, id)); method != "web" || !appliedResult(err) {
		t.Fatalf("retry: %s %v", method, err)
	}
	r.Args["unit"] = "different"
	r = hashedRequest(r)
	if _, err := m.Confirm(context.Background(), nil, withID(r, id)); !errors.Is(err, tool.ErrDenied) {
		t.Fatal("changed arguments used a window")
	}
	if _, err := m.Confirm(context.Background(), nil, withID(r, strings.Repeat("a", 26))); !errors.Is(err, tool.ErrDenied) {
		t.Fatal("unknown ID used a window")
	}
	if method, err := m.Confirm(context.Background(), nil, r); method != "window" || err != nil {
		t.Fatalf("fresh call lost window: %s %v", method, err)
	}
	if runs.Load() != 1 {
		t.Fatal("retry ran again")
	}
}

func TestRetainedExecutionsKeepCapacityBound(t *testing.T) {
	m, mux := newWebManager(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	for range 100 {
		id := pendingID(t, m, approvalRequest())
		decide(t, mux, id, "approve")
	}
	now = now.Add(2 * time.Minute) // Past original TTL, within result retention.
	if _, err := m.Confirm(context.Background(), nil, approvalRequest()); !errors.Is(err, tool.ErrDenied) || len(m.approvals) != 100 {
		t.Fatalf("retained capacity: %v (%d)", err, len(m.approvals))
	}
	now = now.Add(10 * time.Minute)
	pendingID(t, m, approvalRequest())
	if len(m.approvals) != 1 {
		t.Fatalf("expired results retained: %d", len(m.approvals))
	}
}
