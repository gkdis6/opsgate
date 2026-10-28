package approval

import (
	"bytes"
	"encoding/json"
	"os"

	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
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

type fakeBrowser struct{}

func (fakeBrowser) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next(w, r, r.Header.Get("X-Test-User"))
	})
}

var (
	linkID    = regexp.MustCompile(`/approve/([a-z2-7]{26})`)
	csrfValue = regexp.MustCompile(`name="csrf" value="([a-z2-7]+)"`)
)

func newWebManager(t *testing.T) (*Manager, *http.ServeMux) {
	t.Helper()
	m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example/", fakeBrowser{}, nil)
	mux := http.NewServeMux()
	m.Routes(mux)
	return m, mux
}

func pendingID(t *testing.T, m *Manager, r tool.ConfirmRequest) string {
	t.Helper()
	method, err := m.Confirm(context.Background(), nil, hashedRequest(r))
	if method != "web" || !errors.Is(err, tool.ErrApprovalPending) {
		t.Fatalf("Confirm = %q, %v", method, err)
	}
	text := err.Error()
	match := linkID.FindStringSubmatch(text)
	if len(match) != 2 || !strings.Contains(text, "https://ops.example/approve/") ||
		!strings.Contains(text, "call "+r.Tool+" again with the same arguments plus approval_id \""+match[1]+"\"") ||
		!strings.Contains(text, "expires at") {
		t.Fatalf("pending instructions = %q", text)
	}
	return match[1]
}

func webRequest(mux *http.ServeMux, method, id, user string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://ops.example/approve/"+id, strings.NewReader(form.Encode()))
	r.Header.Set("X-Test-User", user)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func readCSRF(t *testing.T, mux *http.ServeMux, id string) string {
	t.Helper()
	w := webRequest(mux, http.MethodGet, id, "github:operator", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	match := csrfValue.FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatalf("missing CSRF token in page: %s", w.Body.String())
	}
	return match[1]
}

func decide(t *testing.T, mux *http.ServeMux, id, decision string) {
	t.Helper()
	csrf := readCSRF(t, mux, id)
	w := webRequest(mux, http.MethodPost, id, "github:operator", url.Values{
		"csrf": {csrf}, "decision": {decision},
	}, map[string]string{"Origin": "https://ops.example"})
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
}

type restartInput struct {
	tool.Approval
	Unit string `json:"unit"`
}

func TestWebEndToEnd(t *testing.T) {
	m, mux := newWebManager(t)
	logger, err := audit.Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	caller := approvalRequest().Caller
	env := &tool.Env{Confirm: m, Audit: logger, Timeout: time.Second, MaxOutputBytes: 4096, Fallback: &caller}
	var applies atomic.Int32
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	tool.AddWrite(s, env, tool.WriteSpec{Name: "restart"}, tool.WriteHandler[restartInput]{
		Plan: func(context.Context, tool.Caller, restartInput) (tool.Plan, error) {
			return tool.Plan{Summary: "Restart nginx.service"}, nil
		},
		Apply: func(context.Context, tool.Caller, restartInput, tool.Plan) (string, error) {
			applies.Add(1)
			return "restarted", nil
		},
	})
	cs := connectApproval(t, s, nil)
	call := func(id string) *mcp.CallToolResult {
		t.Helper()
		args := map[string]any{"unit": "nginx.service"}
		if id != "" {
			args["approval_id"] = id
		}
		result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "restart", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := call("")
	match := linkID.FindStringSubmatch(result.Content[0].(*mcp.TextContent).Text)
	if result.IsError || len(match) != 2 || applies.Load() != 0 {
		t.Fatalf("initial result = %+v; applies %d", result, applies.Load())
	}
	id := match[1]
	if result := call(id); result.IsError || applies.Load() != 0 {
		t.Fatalf("undecided result = %+v; applies %d", result, applies.Load())
	}
	decide(t, mux, id, "approve")
	if result := call(id); result.IsError || applies.Load() != 1 {
		t.Fatalf("approved result = %+v; applies %d", result, applies.Load())
	}
	if result := call(id); !result.IsError || applies.Load() != 1 {
		t.Fatalf("reused result = %+v; applies %d", result, applies.Load())
	}
}

func TestWebBindingAndDecisions(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		mutate                   func(*tool.ConfirmRequest)
		decision                 string
		target                   string
		expire, pending, allowed bool
	}{
		{name: "approved", decision: "approve", allowed: true},
		{name: "undecided", pending: true},
		{name: "rejected", decision: "reject"},
		{name: "expired", decision: "approve", expire: true},
		{name: "changed args", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Args["unit"] = "other.service" }},
		{name: "changed tool", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Tool = "delete" }},
		{name: "wrong user", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Caller.User = "github:other" }},
		{name: "changed summary", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Summary = "Delete nginx.service" }},
		{name: "changed target", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Target = "other cluster" }},
		{name: "changed named target", target: "cluster A", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Target = "cluster B" }},
		{name: "removed target", target: "cluster A", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.Target = "" }},
		{name: "changed noWindow", decision: "approve", mutate: func(r *tool.ConfirmRequest) { r.NoWindow = true }},
		{name: "unknown id", mutate: func(r *tool.ConfirmRequest) { r.ApprovalID = strings.Repeat("a", 26) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, mux := newWebManager(t)
			r := approvalRequest()
			r.Target = tc.target
			id := pendingID(t, m, r)
			if tc.decision != "" {
				decide(t, mux, id, tc.decision)
			}
			if tc.expire {
				m.now = func() time.Time { return time.Now().Add(time.Hour) }
			}
			r.ApprovalID = id
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			method, err := m.Confirm(context.Background(), nil, hashedRequest(r))
			if method != "web" || (err == nil) != tc.allowed || errors.Is(err, tool.ErrApprovalPending) != tc.pending ||
				(!tc.allowed && !tc.pending && !errors.Is(err, tool.ErrDenied)) {
				t.Errorf("Confirm = %q, %v", method, err)
			}
			if tc.expire && len(m.approvals) != 0 {
				t.Errorf("expired records not swept: %d", len(m.approvals))
			}
		})
	}
}

func TestWebPageSecurity(t *testing.T) {
	m, mux := newWebManager(t)
	r := approvalRequest()
	r.Summary = "Restart <script>alert(1)</script>"
	r.Args["extra"] = "<script>args</script>"
	r.Args["password"] = "secret"
	id := pendingID(t, m, r)
	csrf := readCSRF(t, mux, id)
	w := webRequest(mux, http.MethodGet, id, r.Caller.User, nil, nil)
	body := w.Body.String()
	for _, expected := range []string{"restart", "nginx.service", "github:operator via http", "Expires", "&lt;script&gt;", "[REDACTED]"} {
		if !strings.Contains(body, expected) {
			t.Errorf("page missing %q", expected)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "password=secret") {
		t.Error("page contains unescaped HTML or secret")
	}
	for _, tc := range []struct {
		name, method, user string
		form               url.Values
		headers            map[string]string
		want               int
	}{
		{name: "wrong user GET", method: "GET", user: "github:other", want: 403},
		{name: "wrong user POST", method: "POST", user: "github:other", form: url.Values{"csrf": {csrf}, "decision": {"approve"}}, want: 403},
		{name: "wrong user oversized POST", method: "POST", user: "github:other", form: url.Values{"csrf": {csrf}, "decision": {strings.Repeat("a", 4096)}}, want: 403},
		{name: "missing csrf", method: "POST", user: r.Caller.User, form: url.Values{"decision": {"approve"}}, want: 403},
		{name: "wrong csrf", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {"wrong"}, "decision": {"approve"}}, want: 403},
		{name: "duplicate csrf", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf, csrf}, "decision": {"approve"}}, want: 403},
		{name: "invalid decision", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf}, "decision": {"maybe"}}, want: 400},
		{name: "extra fields", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf}, "decision": {"approve"}, "extra": {"x"}}, want: 403},
		{name: "cross origin", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf}, "decision": {"approve"}}, headers: map[string]string{"Origin": "https://attacker.example"}, want: 403},
		{name: "cross site fetch", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf}, "decision": {"approve"}}, headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, want: 403},
		{name: "oversized body", method: "POST", user: r.Caller.User, form: url.Values{"csrf": {csrf}, "decision": {strings.Repeat("a", 4096)}}, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := webRequest(mux, tc.method, id, tc.user, tc.form, tc.headers)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if w.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'; form-action 'self'" ||
				w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("security headers = %v", w.Header())
			}
			if tc.user != r.Caller.User && (strings.Contains(w.Body.String(), "nginx") || strings.Contains(w.Body.String(), csrf)) {
				t.Error("wrong user received approval details")
			}
			r.ApprovalID = id
			if _, err := m.Confirm(context.Background(), nil, hashedRequest(r)); !errors.Is(err, tool.ErrApprovalPending) {
				t.Errorf("invalid POST changed approval: %v", err)
			}
		})
	}
	decide(t, mux, id, "reject")
	w = webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {"approve"}}, nil)
	if w.Code != http.StatusConflict {
		t.Errorf("second decision = %d", w.Code)
	}
}

func TestWebConcurrentConsume(t *testing.T) {
	m, mux := newWebManager(t)
	r := approvalRequest()
	r.ApprovalID = pendingID(t, m, r)
	decide(t, mux, r.ApprovalID, "approve")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := m.Confirm(context.Background(), nil, hashedRequest(r))
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, tool.ErrDenied) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Errorf("approval consumed %d times", successes.Load())
	}
}

type countingBrowser struct{ calls atomic.Int32 }

func (b *countingBrowser) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	b.calls.Add(1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next(w, r, "github:operator")
	})
}

func TestUnknownApprovalDoesNotStartLogin(t *testing.T) {
	browser := new(countingBrowser)
	m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example", browser, nil)
	mux := http.NewServeMux()
	m.Routes(mux)
	id := pendingID(t, m, approvalRequest())
	m.now = func() time.Time { return time.Now().Add(time.Hour) }
	for _, target := range []string{"invalid", strings.Repeat("a", 26), id} {
		for _, method := range []string{"GET", "POST"} {
			w := webRequest(mux, method, target, "", nil, nil)
			if w.Code != http.StatusNotFound || browser.calls.Load() != 0 {
				t.Errorf("%s %s = %d; login calls %d", method, target, w.Code, browser.calls.Load())
			}
		}
	}
}

func TestApprovalDecisionAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example", fakeBrowser{}, logger)
	mux := http.NewServeMux()
	m.Routes(mux)
	var ids []string
	for _, decision := range []string{"approve", "reject"} {
		id := pendingID(t, m, approvalRequest())
		ids = append(ids, id)
		csrf := readCSRF(t, mux, id)
		decide(t, mux, id, decision)
		// A duplicate POST must not create another decision event.
		w := webRequest(mux, "POST", id, "github:operator", url.Values{"csrf": {csrf}, "decision": {decision}}, nil)
		if w.Code != http.StatusConflict {
			t.Fatal(w.Code)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("audit = %s", data)
	}
	for i, outcome := range []string{"approved", "rejected"} {
		var ev audit.Event
		if err := json.Unmarshal(lines[i], &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Outcome != outcome || ev.User != "github:operator" || ev.Tool != "restart" || ev.Approval != "web" || ev.Args["approval_id_prefix"] != ids[i][:8] || strings.Contains(string(data), ids[i]) {
			t.Fatalf("audit = %s", data)
		}
	}
}
