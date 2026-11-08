package approval

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

var noticeKey = []byte("0123456789abcdef")

func noticeManager(t *testing.T) (*Manager, *http.ServeMux) {
	t.Helper()
	m, mux := newWebManager(t)
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, append(noticeKey, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.EnableInboxNotify(config.InboxNotify{SecretFile: path, Subjects: []string{"alice", "bob"}}); err != nil {
		t.Fatal(err)
	}
	return m, mux
}

func noticeBody(t *testing.T, now time.Time, pod string) string {
	t.Helper()
	b, err := json.Marshal(alert.NoticeBatch{SentAt: now, Notices: []alert.Notice{{Cluster: "main", Namespace: "apps", Pod: pod, WorkloadKind: "deployment", WorkloadName: "web", Reason: "CrashLoopBackOff", Severity: alert.Warning, OccurredAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func postNotice(mux *http.ServeMux, body, signature string) *httptest.ResponseRecorder {
	return inboxRequest(mux, "POST", "/approvals/notify", "", "", body, map[string]string{"X-Opsgate-Signature": signature})
}

func TestNoticeTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		delta     time.Duration
		change    func(string) string
		signature string
		code      int
	}{
		{name: "good", code: 204},
		{name: "bad HMAC", signature: "bad", code: 401},
		{name: "missing HMAC", signature: "missing", code: 401},
		{name: "stale", delta: -5 * time.Minute, code: 400},
		{name: "future", delta: time.Minute + time.Second, code: 400},
		{name: "future tolerance", delta: time.Minute, code: 204},
		{name: "oversized", change: func(b string) string { return b + strings.Repeat(" ", 64<<10) }, code: 413},
		{name: "unknown field", change: func(b string) string { return strings.Replace(b, `"cluster":`, `"summary":"secret", "cluster":`, 1) }, code: 400},
		{name: "info", change: func(b string) string { return strings.Replace(b, "warning", "info", 1) }, code: 400},
		{name: "path traversal", change: func(b string) string { return strings.Replace(b, "web-pod", "..", 1) }, code: 400},
		{name: "free text", change: func(b string) string { return strings.Replace(b, "CrashLoopBackOff", "cluster says SECRET", 1) }, code: 400},
		{name: "external hint", change: func(b string) string { return strings.Replace(b, `"deployment"`, `"https://evil.example"`, 1) }, code: 400},
		{name: "multiple JSON", change: func(b string) string { return b + `{}` }, code: 400},
		{name: "no notices", change: func(string) string { return `{"sent_at":"2026-11-08T00:00:00Z","notices":[]}` }, code: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, mux := noticeManager(t)
			now := time.Now().UTC().Truncate(time.Second)
			m.now = func() time.Time { return now }
			body := noticeBody(t, now.Add(tc.delta), "web-pod")
			if tc.change != nil {
				body = tc.change(body)
			}
			signature := alert.Sign(noticeKey, []byte(body))
			if tc.signature == "bad" {
				signature = alert.Sign([]byte("another key"), []byte(body))
			}
			if tc.signature == "missing" {
				signature = ""
			}
			w := postNotice(mux, body, signature)
			if w.Code != tc.code {
				t.Fatalf("got %d want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "web-pod") || strings.Contains(w.Body.String(), "SECRET") {
				t.Fatal("input reflected")
			}
			if len(m.approvals) != 0 {
				t.Fatal("notice created an approval")
			}
			if tc.code != 204 && len(m.notices.users) != 0 {
				t.Fatal("rejected notice stored")
			}
		})
	}
}

func TestNoticeReplayRateAndExpiry(t *testing.T) {
	m, mux := noticeManager(t)
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	body := noticeBody(t, now, "web-pod")
	sig := alert.Sign(noticeKey, []byte(body))
	if postNotice(mux, body, sig).Code != 204 || postNotice(mux, body, sig).Code != 409 {
		t.Fatal("replay not blocked")
	}
	alternate := "sha256=" + strings.ToUpper(sig[7:])
	if postNotice(mux, body, alternate).Code != 409 {
		t.Fatal("alternate hex bypassed replay cache")
	}
	for range 57 {
		postNotice(mux, body, "")
	}
	if postNotice(mux, body, sig).Code != 429 {
		t.Fatal("unauthenticated requests not rate limited")
	}
	now = now.Add(6 * time.Minute)
	body = noticeBody(t, now, "web-new")
	if postNotice(mux, body, alert.Sign(noticeKey, []byte(body))).Code != 204 || len(m.notices.replay) != 1 {
		t.Fatal("rate/replay expiry failed")
	}
}

func TestNoticeInboxIsolationDismissAndRetention(t *testing.T) {
	m, mux := noticeManager(t)
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	body := noticeBody(t, now, "web-pod")
	if w := postNotice(mux, body, alert.Sign(noticeKey, []byte(body))); w.Code != 204 {
		t.Fatal(w.Code)
	}
	for _, user := range []string{"alice", "bob", "charlie"} {
		w := inboxRequest(mux, "GET", "/approvals", user, "", "", nil)
		if strings.Contains(w.Body.String(), "web-pod") != (user != "charlie") {
			t.Fatal("notice subject leak", user)
		}
		if user == "alice" && (!strings.Contains(w.Body.String(), "Alerts waiting for a decision") || !strings.Contains(w.Body.String(), "/approve/options/main/apps/web-pod?workload=deployment%2Fweb")) {
			t.Fatal("missing notice navigation", w.Body.String())
		}
	}
	now = now.Add(time.Second)
	body = noticeBody(t, now, "web-pod")
	postNotice(mux, body, alert.Sign(noticeKey, []byte(body)))
	if len(m.notices.users["alice"]) != 1 || !m.notices.users["alice"][0].OccurredAt.Equal(now) {
		t.Fatal("repeat not updated")
	}
	id := m.notices.users["alice"][0].ID
	dismiss := func(user, csrf string, headers map[string]string) int {
		r := httptest.NewRequest("POST", "https://ops.example/approvals/dismiss", strings.NewReader(url.Values{"id": {id}, "csrf": {csrf}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Test-User", user)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if dismiss("alice", "", nil) != 403 || dismiss("alice", m.inboxCSRF("bob"), nil) != 403 || dismiss("alice", m.inboxCSRF("alice"), map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}) != 403 {
		t.Fatal("dismiss CSRF missing")
	}
	if len(m.notices.users["alice"]) != 1 {
		t.Fatal("invalid CSRF dismissed notice")
	}
	if dismiss("alice", m.inboxCSRF("alice"), nil) != 303 || len(m.notices.users["alice"]) != 0 || len(m.notices.users["bob"]) != 1 {
		t.Fatal("dismiss not isolated")
	}
	for i := 0; i < 55; i++ {
		now = now.Add(time.Second)
		body = noticeBody(t, now, fmt.Sprintf("pod-%d", i))
		if postNotice(mux, body, alert.Sign(noticeKey, []byte(body))).Code != 204 {
			t.Fatal("could not add notice", i)
		}
	}
	if len(m.notices.users["bob"]) != 50 || m.notices.users["bob"][0].Pod != "pod-5" {
		t.Fatal("notice capacity/eviction")
	}
	now = now.Add(time.Hour)
	inboxRequest(mux, "GET", "/approvals", "bob", "", "", nil)
	if len(m.notices.users["bob"]) != 0 {
		t.Fatal("notice retention")
	}
}

func TestNoticePushCoalescingAndLanguage(t *testing.T) {
	m, mux := noticeManager(t)
	m.push = testPush(t) // Keep delivery queued to assert one job per subject.
	now := time.Now().UTC()
	m.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		body := noticeBody(t, now, fmt.Sprintf("pod-%d", i))
		if postNotice(mux, body, alert.Sign(noticeKey, []byte(body))).Code != 204 {
			t.Fatal("notice rejected")
		}
		now = now.Add(20 * time.Second)
	}
	if len(m.push.jobs) != 2 {
		t.Fatal("push not coalesced", len(m.push.jobs))
	}
	body := noticeBody(t, now, "pod-next")
	postNotice(mux, body, alert.Sign(noticeKey, []byte(body)))
	if len(m.push.jobs) != 4 {
		t.Fatal("push not renewed after minute")
	}
	for _, lang := range []string{"en", "ko"} {
		m.push.cfg.Language = lang
		want := "An alert needs a decision"
		if lang == "ko" {
			want = "알림에 대한 조치가 필요합니다"
		}
		w := inboxRequest(mux, "GET", "/approvals/sw.js", "", "", "", nil)
		if !strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), "event.data") {
			t.Fatal("worker message", w.Body.String())
		}
	}
}

type noticeDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *noticeDeadlineRecorder) SetReadDeadline(t time.Time) error {
	w.deadlines = append(w.deadlines, t)
	return nil
}

func TestNoticeUploadDeadlineAndDisabled(t *testing.T) {
	m, mux := noticeManager(t)
	now := time.Now().UTC()
	body := noticeBody(t, now, "web-pod")
	r := httptest.NewRequest("POST", "https://ops.example/approvals/notify", strings.NewReader(body))
	r.Header.Set("X-Opsgate-Signature", alert.Sign(noticeKey, []byte(body)))
	w := &noticeDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	mux.ServeHTTP(w, r)
	if w.Code != 204 || len(w.deadlines) != 2 || w.deadlines[0].Before(now.Add(4*time.Second)) || !w.deadlines[1].IsZero() {
		t.Fatal("notice upload unbounded", w.Code, w.deadlines)
	}
	m.notices = nil
	if postNotice(mux, body, alert.Sign(noticeKey, []byte(body))).Code != 404 {
		t.Fatal("disabled channel accepts notices")
	}
}

func TestNoticeSignatureHeaderAmbiguity(t *testing.T) {
	_, mux := noticeManager(t)
	body := noticeBody(t, time.Now().UTC(), "web-pod")
	r := httptest.NewRequest("POST", "https://ops.example/approvals/notify", strings.NewReader(body))
	signature := alert.Sign(noticeKey, []byte(body))
	r.Header.Add("X-Opsgate-Signature", signature)
	r.Header.Add("X-Opsgate-Signature", signature)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("multiple signatures accepted", w.Code)
	}
}

func TestNoticeBatchCountAndStartupKeyBounds(t *testing.T) {
	m, mux := noticeManager(t)
	now := time.Now().UTC()
	var batch alert.NoticeBatch
	if err := json.Unmarshal([]byte(noticeBody(t, now, "web-pod")), &batch); err != nil {
		t.Fatal(err)
	}
	for len(batch.Notices) < 51 {
		batch.Notices = append(batch.Notices, batch.Notices[0])
	}
	b, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if postNotice(mux, string(b), alert.Sign(noticeKey, b)).Code != 400 {
		t.Fatal("oversized notice list accepted")
	}
	path := filepath.Join(t.TempDir(), "key")
	for _, value := range []string{"missing", "short"} {
		if value != "missing" {
			if err := os.WriteFile(path, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if m.EnableInboxNotify(config.InboxNotify{SecretFile: path, Subjects: []string{"alice"}}) == nil {
			t.Fatal("bad server key accepted")
		}
	}
}

func TestNoticeRejectedUploadRemainsBounded(t *testing.T) {
	m, mux := noticeManager(t)
	m.notices.requests = 60
	m.notices.rateStart = m.now()
	r := httptest.NewRequest("POST", "https://ops.example/approvals/notify", strings.NewReader("unfinished upload"))
	w := &noticeDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	mux.ServeHTTP(w, r)
	if w.Code != 429 || len(w.deadlines) != 1 || w.deadlines[0].IsZero() || w.Header().Get("Connection") != "close" {
		t.Fatal("rejected upload may hold a connection", w.Code, w.deadlines, w.Header())
	}
}
