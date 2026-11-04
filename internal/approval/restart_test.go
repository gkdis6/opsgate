package approval

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

type accessBrowser struct {
	fakeBrowser
	scopes, tools []string
	known         bool
}

func (b accessBrowser) Access(string) ([]string, []string, bool) { return b.scopes, b.tools, b.known }

type linkRestartInput struct {
	tool.Approval
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type restartFixture struct {
	mux     *http.ServeMux
	manager *Manager
	applied atomic.Int32
	planned atomic.Int32
	summary atomic.Value
	last    atomic.Value
}

func newRestartFixture(t *testing.T, browser BrowserAuth) *restartFixture {
	t.Helper()
	f := &restartFixture{}
	f.summary.Store("Restart deployment apps/web")
	env := &tool.Env{Timeout: time.Second, MaxOutputBytes: 4096}
	tool.AddWrite(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), env,
		tool.WriteSpec{Name: restartTool, Title: "Restart workload", Direct: true},
		tool.WriteHandler[linkRestartInput]{
			Plan: func(_ context.Context, _ tool.Caller, in linkRestartInput) (tool.Plan, error) {
				f.planned.Add(1)
				if in.Namespace == "denied" {
					return tool.Plan{}, tool.Denied("resource is not allowed")
				}
				return tool.Plan{Summary: f.summary.Load().(string), Target: "cluster " + in.Cluster}, nil
			},
			Apply: func(_ context.Context, _ tool.Caller, in linkRestartInput, _ tool.Plan) (string, error) {
				f.applied.Add(1)
				f.last.Store(in)
				return "Requested restart", nil
			},
		})
	f.manager = New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true, Window: time.Hour, WindowTools: []string{restartTool}}, "https://ops.example", browser, nil)
	f.manager.SetDirect(env.Direct)
	f.mux = http.NewServeMux()
	f.manager.Routes(f.mux)
	return f
}

func writer() accessBrowser {
	return accessBrowser{scopes: []string{config.ScopeRead, config.ScopeWrite}, known: true}
}

func (f *restartFixture) do(method, target, user string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("X-Test-User", user)
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

// open follows the link to the approval page and returns its path and token.
func (f *restartFixture) open(t *testing.T, user string) (path, csrf string) {
	t.Helper()
	w := f.do("GET", RestartURL("", "main", "deployment", "apps", "web"), user, nil)
	if w.Code != http.StatusSeeOther || !linkID.MatchString(w.Header().Get("Location")) {
		t.Fatalf("open: %d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	path = w.Header().Get("Location")
	page := f.do("GET", path, user, nil)
	match := csrfValue.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || match == nil {
		t.Fatalf("page: %d %s", page.Code, page.Body)
	}
	if !strings.Contains(page.Body.String(), "Restart deployment apps/web") {
		t.Fatalf("page does not show the plan: %s", page.Body)
	}
	if strings.Contains(page.Body.String(), "approve_window") {
		t.Fatalf("restart link offers an approval window: %s", page.Body)
	}
	return path, match[1]
}

func TestRestartLinkAppliesOnlyAfterApproval(t *testing.T) {
	f := newRestartFixture(t, writer())
	path, csrf := f.open(t, "alice")
	if f.applied.Load() != 0 {
		t.Fatal("opening the link applied the write")
	}
	w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Requested restart") || strings.Contains(w.Body.String(), "Return to your chat") {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	if f.applied.Load() != 1 {
		t.Fatalf("applied %d times", f.applied.Load())
	}
	got := f.last.Load().(linkRestartInput)
	if got.Cluster != "main" || got.Kind != "deployment" || got.Namespace != "apps" || got.Name != "web" {
		t.Fatalf("applied to %+v", got)
	}
	if w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}}); w.Code != http.StatusConflict || f.applied.Load() != 1 {
		t.Fatalf("second approve: %d, applied %d", w.Code, f.applied.Load())
	}
	if w := f.do("GET", path, "alice", nil); !strings.Contains(w.Body.String(), "Requested restart") || f.applied.Load() != 1 {
		t.Fatalf("reload: applied %d, %s", f.applied.Load(), w.Body)
	}
}

func TestRestartLinkRefusals(t *testing.T) {
	t.Run("reject", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		path, csrf := f.open(t, "alice")
		if w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"reject"}}); !strings.Contains(w.Body.String(), "Rejected") || f.applied.Load() != 0 {
			t.Fatalf("reject: applied %d, %s", f.applied.Load(), w.Body)
		}
	})
	t.Run("another user cannot approve", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		path, csrf := f.open(t, "alice")
		if w := f.do("POST", path, "mallory", url.Values{"csrf": {csrf}, "decision": {"approve"}}); w.Code != http.StatusForbidden || f.applied.Load() != 0 {
			t.Fatalf("other user: %d, applied %d", w.Code, f.applied.Load())
		}
	})
	t.Run("wrong csrf", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		path, _ := f.open(t, "alice")
		if w := f.do("POST", path, "alice", url.Values{"csrf": {"wrong"}, "decision": {"approve"}}); w.Code != http.StatusForbidden || f.applied.Load() != 0 {
			t.Fatalf("csrf: %d, applied %d", w.Code, f.applied.Load())
		}
	})
	t.Run("approval window cannot be chosen", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		path, csrf := f.open(t, "alice")
		if w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve_window"}}); w.Code != http.StatusBadRequest || f.applied.Load() != 0 {
			t.Fatalf("window: %d, applied %d", w.Code, f.applied.Load())
		}
	})
	t.Run("plan changed after the page opened", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		path, csrf := f.open(t, "alice")
		f.summary.Store("Restart deployment apps/web (replaced)")
		w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}})
		if !strings.Contains(w.Body.String(), "Not applied") || strings.Contains(w.Body.String(), "Approved") || f.applied.Load() != 0 {
			t.Fatalf("changed: applied %d, %s", f.applied.Load(), w.Body)
		}
	})
	for _, tc := range []struct {
		name    string
		browser BrowserAuth
		target  string
		code    int
	}{
		{"no write scope", accessBrowser{scopes: []string{config.ScopeRead}, known: true}, "/approve/restart/main/deployment/apps/web", http.StatusForbidden},
		{"tool not allowed for user", accessBrowser{scopes: []string{config.ScopeWrite}, tools: []string{"note_*"}, known: true}, "/approve/restart/main/deployment/apps/web", http.StatusForbidden},
		{"unknown user", accessBrowser{scopes: []string{config.ScopeWrite}}, "/approve/restart/main/deployment/apps/web", http.StatusForbidden},
		{"target denied by plan", writer(), "/approve/restart/main/deployment/denied/web", http.StatusForbidden},
		{"sign-in cannot report access", fakeBrowser{}, "/approve/restart/main/deployment/apps/web", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestartFixture(t, tc.browser)
			if w := f.do("GET", tc.target, "alice", nil); w.Code != tc.code || f.applied.Load() != 0 || len(f.manager.approvals) != 0 {
				t.Fatalf("code %d, applied %d, approvals %d: %s", w.Code, f.applied.Load(), len(f.manager.approvals), w.Body)
			}
		})
	}
	t.Run("post cannot start a restart", func(t *testing.T) {
		f := newRestartFixture(t, writer())
		if w := f.do("POST", "/approve/restart/main/deployment/apps/web", "alice", url.Values{}); w.Code != http.StatusMethodNotAllowed || f.planned.Load() != 0 {
			t.Fatalf("post: %d, planned %d", w.Code, f.planned.Load())
		}
	})
	t.Run("tool not registered for direct use", func(t *testing.T) {
		m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example", writer(), nil)
		m.SetDirect((&tool.Env{}).Direct)
		mux := http.NewServeMux()
		m.Routes(mux)
		r := httptest.NewRequest("GET", "/approve/restart/main/deployment/apps/web", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("code %d", w.Code)
		}
	})
}

func TestRestartApprovalCannotBeClaimedByClient(t *testing.T) {
	f := newRestartFixture(t, writer())
	path, csrf := f.open(t, "alice")
	id := strings.TrimPrefix(path, "/approve/")
	f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"reject"}})
	f.manager.mu.Lock()
	f.manager.approvals[id].decision = "approved"
	f.manager.approvals[id].transport = "http"
	f.manager.mu.Unlock()
	_, err := f.manager.Confirm(context.Background(), nil, tool.ConfirmRequest{
		Caller: tool.Caller{User: "alice", Transport: "http"}, Tool: restartTool, Summary: "Restart deployment apps/web", Target: "cluster main", NoWindow: true, ApprovalID: id, MaxArgBytes: 1 << 10,
	})
	if !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("Confirm = %v", err)
	}
}

func TestRestartURLEscapesAndRoutes(t *testing.T) {
	if got := RestartURL("https://ops.example/", "a/b", "deployment", "apps", "we b"); got != "https://ops.example/approve/restart/a%2Fb/deployment/apps/we%20b" {
		t.Fatalf("url = %q", got)
	}
	f := newRestartFixture(t, writer())
	w := f.do("GET", RestartURL("", "a/b", "deployment", "apps", "web"), "alice", nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	id := strings.TrimPrefix(w.Header().Get("Location"), "/approve/")
	if !strings.Contains(string(f.manager.approvals[id].args), `"cluster":"a/b"`) {
		t.Fatalf("args = %s", f.manager.approvals[id].args)
	}
}
