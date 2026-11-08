package approval

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func inboxRequest(mux *http.ServeMux, method, path, user, csrf, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://ops.example"+path, strings.NewReader(body))
	r.Header.Set("X-Test-User", user)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestInboxIsolationOrderAndEscaping(t *testing.T) {
	m, mux := newWebManager(t)
	request := approvalRequest()
	request.Title = "Decided <script>"
	decided := pendingID(t, m, request)
	m.mu.Lock()
	m.approvals[decided].decision = "approved"
	m.approvals[decided].finished = true
	m.approvals[decided].result = "result <script>"
	m.mu.Unlock()
	request.Title = "Pending <phone>"
	request.Target = "cluster public"
	pending := pendingID(t, m, request)
	m.mu.Lock()
	m.approvals[pending].source = "ai"
	m.mu.Unlock()
	request.Title = "Other user secret"
	request.Caller.User = "another-user"
	other := pendingID(t, m, request)
	w := inboxRequest(mux, "GET", "/approvals", "github:operator", "", "", nil)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, other) || strings.Contains(body, "Other user secret") {
		t.Fatal("inbox leaks another user's records", w.Code, body)
	}
	for _, want := range []string{pending, decided, "Pending &lt;phone&gt;", "result &lt;script&gt;", "cluster public", "AI — review carefully", "Requested", "Expires", "applied", "pending", `name="viewport"`, `rel="manifest"`, `src="/approvals/inbox.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("inbox missing %q", want)
		}
	}
	if strings.Index(body, pending) > strings.Index(body, decided) {
		t.Fatal("pending does not come first")
	}
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	empty := inboxRequest(mux, "GET", "/approvals", "github:operator", "", "", nil).Body.String()
	if strings.Contains(empty, pending) || strings.Contains(empty, decided) || !strings.Contains(empty, "No live approvals") {
		t.Fatal("expired records in inbox")
	}
}

func TestInboxCSPAndInstallAssets(t *testing.T) {
	m, mux := newWebManager(t)
	for _, path := range []string{"/approvals", "/approvals/inbox.js", "/approvals/sw.js", "/approvals/manifest.webmanifest"} {
		w := inboxRequest(mux, "GET", path, "github:operator", "", "", nil)
		if w.Code != 200 {
			t.Fatalf("asset %s = %d", path, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, directive := range []string{"default-src 'none'", "script-src 'self'", "worker-src 'self'", "manifest-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, directive) {
				t.Errorf("missing CSP %s on %s", directive, path)
			}
		}
		if strings.Contains(csp, "script-src 'unsafe-inline'") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("unsafe inbox headers")
		}
	}
	id := pendingID(t, m, approvalRequest())
	csp := webRequest(mux, "GET", id, "github:operator", nil, nil).Header().Get("Content-Security-Policy")
	for _, relaxed := range []string{"script-src", "worker-src", "manifest-src", "connect-src"} {
		if strings.Contains(csp, relaxed) {
			t.Fatal("approval page CSP relaxed", csp)
		}
	}
	w := inboxRequest(mux, "GET", "/approvals/manifest.webmanifest", "", "", "", nil)
	var manifest struct {
		Name, Display string
		StartURL      string `json:"start_url"`
		Icons         []struct{ Src string }
	}
	if json.Unmarshal(w.Body.Bytes(), &manifest) != nil || manifest.Name != "opsgate" || manifest.StartURL != "/approvals" || manifest.Display != "standalone" || len(manifest.Icons) != 1 {
		t.Fatal("bad manifest", w.Body.String())
	}
	icon := inboxRequest(mux, "GET", manifest.Icons[0].Src, "", "", "", nil)
	image, err := png.DecodeConfig(icon.Body)
	if err != nil || image.Width != 512 || image.Height != 512 {
		t.Fatal("install icon missing or wrong size")
	}
	worker := inboxRequest(mux, "GET", "/approvals/sw.js", "", "", "", nil)
	if worker.Header().Get("Service-Worker-Allowed") != "/approvals" || !strings.Contains(worker.Body.String(), "An approval is waiting") || strings.Contains(worker.Body.String(), "event.data") {
		t.Fatal("worker scope or generic message incorrect")
	}
	m.push = testPush(t)
	m.push.cfg.Language = "ko"
	if !strings.Contains(inboxRequest(mux, "GET", "/approvals/sw.js", "", "", "", nil).Body.String(), "승인 요청이 있습니다") {
		t.Fatal("missing Korean notification")
	}
}

func TestPushSubscriptionSecurityAndIsolation(t *testing.T) {
	m, mux := newWebManager(t)
	m.browser = writer()
	m.push = testPush(t)
	body := `{"endpoint":"https://fcm.googleapis.com/device-secret","expirationTime":null,"keys":{"p256dh":"device-key","auth":"auth-key"}}`
	post := func(user, token, operation, body string, headers map[string]string) *httptest.ResponseRecorder {
		return inboxRequest(mux, "POST", "/approvals/push/"+operation, user, token, body, headers)
	}
	for _, tc := range []struct {
		name, user, token, body string
		headers                 map[string]string
		code                    int
	}{
		{name: "missing CSRF", user: "alice", body: body, code: 403},
		{name: "other user CSRF", user: "bob", token: m.inboxCSRF("alice"), body: body, code: 403},
		{name: "cross origin", user: "alice", token: m.inboxCSRF("alice"), body: body, headers: map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, code: 403},
		{name: "oversized", user: "alice", token: m.inboxCSRF("alice"), body: body + strings.Repeat(" ", 4096), code: 400},
		{name: "trailing JSON", user: "alice", token: m.inboxCSRF("alice"), body: body + `{}`, code: 400},
		{name: "disallowed endpoint", user: "alice", token: m.inboxCSRF("alice"), body: `{"endpoint":"https://evil.example/secret"}`, code: 400},
		{name: "private endpoint", user: "alice", token: m.inboxCSRF("alice"), body: `{"endpoint":"https://127.0.0.1/secret"}`, code: 400},
	} {
		for _, operation := range []string{"subscribe", "unsubscribe"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				w := post(tc.user, tc.token, operation, tc.body, tc.headers)
				if w.Code != tc.code || strings.Contains(w.Body.String(), "device-secret") {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			})
		}
	}
	if len(m.push.subscriptions("alice")) != 0 || len(m.push.subscriptions("bob")) != 0 {
		t.Fatal("attack changed subscriptions")
	}
	for _, user := range []string{"alice", "bob"} {
		if w := post(user, m.inboxCSRF(user), "subscribe", body, nil); w.Code != 204 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := post("bob", m.inboxCSRF("bob"), "unsubscribe", body, nil)
	if w.Code != 204 || len(m.push.subscriptions("bob")) != 0 || len(m.push.subscriptions("alice")) != 1 {
		t.Fatal("unsubscribe isolation failed", w.Code)
	}
	for _, b := range []BrowserAuth{fakeBrowser{}, accessBrowser{known: true}, accessBrowser{scopes: []string{"ops:write"}, known: false}} {
		m.browser = b
		if w := post("alice", m.inboxCSRF("alice"), "subscribe", body, nil); w.Code != 403 {
			t.Fatal("non approver subscribed", w.Code)
		}
	}
	m.browser = writer()
	page := inboxRequest(mux, "GET", "/approvals", "alice", "", "", nil)
	if !strings.Contains(page.Body.String(), "Enable notifications on this device") || !strings.Contains(page.Body.String(), "Add to Home Screen") || strings.Contains(page.Body.String(), "device-secret") {
		t.Fatal("notification controls missing or leak endpoint")
	}
	key := inboxRequest(mux, "GET", "/approvals/push/key", "alice", "", "", nil)
	if key.Code != 200 || !strings.Contains(key.Body.String(), m.push.publicKey) {
		t.Fatal("missing public key")
	}
	m.push = nil
	if w := post("alice", m.inboxCSRF("alice"), "subscribe", body, nil); w.Code != 404 {
		t.Fatal("push enabled without config")
	}
}

func TestInboxUsesBrowserAuthentication(t *testing.T) {
	m, mux := newWebManager(t)
	browser := new(countingBrowser)
	m.browser = browser
	w := inboxRequest(mux, "GET", "/approvals", "", "", "", nil)
	if w.Code != http.StatusOK || browser.calls.Load() != 1 {
		t.Fatal("inbox bypassed BrowserAuth.RequireUser")
	}
}

func TestNextOpensNewestWaitingApproval(t *testing.T) {
	m, mux := newWebManager(t)
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	now := base
	m.now = func() time.Time { return now }
	next := func(user string) string {
		w := inboxRequest(mux, "GET", "/approvals/next", user, "", "", nil)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("next: %d %s", w.Code, w.Body)
		}
		return w.Header().Get("Location")
	}
	if got := next("github:operator"); got != "/approvals" {
		t.Fatalf("empty inbox: %s", got)
	}
	older := pendingID(t, m, distinctRequest(1))
	now = base.Add(time.Second)
	newer := pendingID(t, m, distinctRequest(2))
	if got := next("github:operator"); got != "/approve/"+newer {
		t.Fatalf("want newest %s, got %s", newer, got)
	}
	if got := next("github:other"); got != "/approvals" {
		t.Fatalf("another user's approval leaked: %s", got)
	}
	csrf := readCSRF(t, mux, newer)
	webRequest(mux, "POST", newer, "github:operator", map[string][]string{"csrf": {csrf}, "decision": {"reject"}}, nil)
	if got := next("github:operator"); got != "/approve/"+older {
		t.Fatalf("decided approval still chosen: %s", got)
	}
}
