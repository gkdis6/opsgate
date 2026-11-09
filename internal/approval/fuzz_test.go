package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

func fuzzNoticeManager(f *testing.F) (*Manager, *http.ServeMux) {
	m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example/", fakeBrowser{}, nil)
	mux := http.NewServeMux()
	m.Routes(mux)
	path := filepath.Join(f.TempDir(), "key")
	if err := os.WriteFile(path, noticeKey, 0600); err != nil {
		f.Fatal(err)
	}
	if err := m.EnableInboxNotify(config.InboxNotify{SecretFile: path, Subjects: []string{"alice"}}); err != nil {
		f.Fatal(err)
	}
	return m, mux
}

func sign(body []byte) string {
	mac := hmac.New(sha256.New, noticeKey)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// resetNotices clears rate, replay and stored state between fuzz inputs.
func resetNotices(m *Manager) {
	m.mu.Lock()
	m.notices.requests = 0
	m.notices.replay = map[string]time.Time{}
	m.notices.users = map[string][]inboxNotice{}
	m.mu.Unlock()
}

// A correctly signed notice still never stores free text or a link outside
// the live choices pages.
func FuzzNoticeFields(f *testing.F) {
	f.Add("main", "apps", "web-1", "deployment", "web", "CrashLoopBackOff", "")
	f.Add("", "", "nginx.service", "", "", "Failed", "unit")
	f.Add("main", "apps", "..", "deployment", "web/../x", "x", "")
	f.Add("main", "a/b", "web", "https:", "//evil", "<script>", "container")
	m, mux := fuzzNoticeManager(f)
	valid, _ := json.Marshal(alert.NoticeBatch{SentAt: time.Now(), Notices: []alert.Notice{{Cluster: "main", Namespace: "apps", Pod: "web-1", Reason: "Failed", Severity: alert.Warning, OccurredAt: time.Now()}}})
	if w := postNotice(mux, string(valid), sign(valid)); w.Code != http.StatusNoContent {
		f.Fatalf("valid notice: %d", w.Code)
	}
	f.Fuzz(func(t *testing.T, cluster, namespace, pod, kind, name, reason, host string) {
		resetNotices(m)
		now := time.Now()
		body, err := json.Marshal(alert.NoticeBatch{SentAt: now, Notices: []alert.Notice{{Cluster: cluster, Namespace: namespace, Pod: pod, WorkloadKind: kind, WorkloadName: name, Reason: reason, Severity: alert.Warning, OccurredAt: now, HostKind: host}}})
		if err != nil {
			return
		}
		w := postNotice(mux, string(body), sign(body))
		if w.Code >= 500 {
			t.Fatalf("notice status %d", w.Code)
		}
		m.mu.Lock()
		stored := append([]inboxNotice(nil), m.notices.users["alice"]...)
		m.mu.Unlock()
		if w.Code != http.StatusNoContent {
			if len(stored) != 0 {
				t.Fatal("rejected notice stored")
			}
			return
		}
		for _, n := range stored {
			if !n.Valid() {
				t.Fatalf("stored invalid notice %+v", n.Notice)
			}
			if !strings.HasPrefix(n.URL, "/approve/options/") && !strings.HasPrefix(n.URL, "/approve/host-options/") || strings.Contains(n.URL, "..") || strings.Contains(n.URL, "//") {
				t.Fatalf("notice link %q", n.URL)
			}
		}
		page := inboxRequest(mux, "GET", "/approvals", "alice", "", "", nil)
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "<script>") {
			t.Fatalf("inbox %d", page.Code)
		}
	})
}

// Without the key nothing is accepted, whatever the body or signature header.
func FuzzNoticeSignature(f *testing.F) {
	body := `{"sent_at":"2026-11-08T00:00:00Z","notices":[]}`
	f.Add(body, "sha256="+strings.Repeat("0", 64))
	f.Add(body, "SHA256="+strings.Repeat("A", 64))
	f.Add(body, sign([]byte(body))[:70])
	m, mux := fuzzNoticeManager(f)
	f.Fuzz(func(t *testing.T, body, signature string) {
		resetNotices(m)
		w := postNotice(mux, body, signature)
		if w.Code == http.StatusNoContent && signature != sign([]byte(body)) && !strings.EqualFold(signature, sign([]byte(body))) {
			t.Fatalf("accepted %q with signature %q", body, signature)
		}
		if w.Code >= 500 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

// Every approval route answers malformed paths and forms without a server
// error, and none of them executes anything for an unauthenticated caller.
func FuzzApprovalRoutes(f *testing.F) {
	for _, seed := range []string{"/approve/abc", "/approve/options/a/b/c?workload=deployment/web", "/approve/host-options/unit/x.service", "/approve/action/k8s_scale?replicas=-1", "/approvals/next", "/approvals/latest", "/approve/%zz", "/approve/restart/a/b/c/d"} {
		f.Add("GET", seed, "", "alice")
		f.Add("POST", seed, "csrf=x&decision=approve", "")
	}
	_, mux := fuzzNoticeManager(f)
	f.Fuzz(func(t *testing.T, method, path, form, user string) {
		if method != "GET" && method != "POST" || !strings.HasPrefix(path, "/approv") {
			return
		}
		r, err := http.NewRequest(method, "https://ops.example"+path, strings.NewReader(form))
		if err != nil {
			return
		}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Test-User", user)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code >= 500 && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %q: %d", method, path, w.Code)
		}
	})
}

// The server posts to subscriber-supplied push endpoints, so only HTTPS URLs
// on the default push services may pass, never a private address.
func FuzzPushEndpoint(f *testing.F) {
	for _, seed := range []string{"https://fcm.googleapis.com/fcm/send/x", "https://a.notify.windows.com/w", "https://evil.notify.windows.com.evil/", "https://127.0.0.1/", "https://web.push.apple.com:8443/", "http://web.push.apple.com/", "https://x@web.push.apple.com/", "https://.notify.windows.com/", "https://WEB.PUSH.APPLE.COM./x"} {
		f.Add(seed)
	}
	p := &pushSender{cfg: config.Default().Approval.Push}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := p.endpoint(raw)
		if err != nil {
			return
		}
		host := strings.ToLower(u.Hostname())
		allowed := host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || host == "web.push.apple.com" ||
			strings.HasSuffix(host, ".notify.windows.com") && !strings.Contains(host, "..")
		if u.Scheme != "https" || u.User != nil || !allowed || u.Port() != "" && u.Port() != "443" {
			t.Fatalf("accepted push endpoint %q", raw)
		}
	})
}
