package approval

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

// restart builds a second manager over the same state file and tools, as a
// new process would.
func (f *restartFixture) restart(t *testing.T, path string) []byte {
	t.Helper()
	direct := f.manager.direct
	f.manager = New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true}, "https://ops.example", writer(), nil)
	key, err := f.manager.EnableState(path)
	if err != nil {
		t.Fatal(err)
	}
	f.manager.SetDirect(direct)
	f.mux = http.NewServeMux()
	f.manager.Routes(f.mux)
	return key
}

func TestLinkApprovalSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "approvals.json")
	f := newRestartFixture(t, writer())
	key, err := f.manager.EnableState(path)
	if err != nil || len(key) != 32 {
		t.Fatal(err)
	}
	waiting, oldCSRF := f.open(t, "alice")
	decided, csrf := f.openTarget(t, "alice", "other")
	if w := f.do("POST", decided, "alice", url.Values{"csrf": {csrf}, "decision": {"reject"}}); w.Code != http.StatusOK {
		t.Fatalf("reject: %d", w.Code)
	}
	info, err := os.Stat(path)
	raw, _ := os.ReadFile(path)
	if err != nil || info.Mode().Perm() != 0600 || bytes.Contains(raw, []byte(oldCSRF)) {
		t.Fatalf("state file: %v, mode %v, holds csrf %t", err, info.Mode(), bytes.Contains(raw, []byte(oldCSRF)))
	}

	if again := f.restart(t, path); !bytes.Equal(again, key) {
		t.Fatal("link key changed across restart")
	}
	if w := f.do("GET", decided, "alice", nil); w.Code != http.StatusNotFound {
		t.Fatalf("decided approval restored: %d", w.Code)
	}
	if w := f.do("GET", waiting, "mallory", nil); w.Code != http.StatusForbidden {
		t.Fatalf("another user after restart: %d", w.Code)
	}
	page := f.do("GET", waiting, "alice", nil)
	match := csrfValue.FindStringSubmatch(page.Body.String())
	if page.Code != http.StatusOK || len(match) != 2 || match[1] == oldCSRF || !strings.Contains(page.Body.String(), "Restart deployment apps/web") {
		t.Fatalf("restored page: %d %s", page.Code, page.Body)
	}
	// The token from before the restart no longer works; the new one does.
	if w := f.do("POST", waiting, "alice", url.Values{"csrf": {oldCSRF}, "decision": {"approve"}}); w.Code != http.StatusForbidden || f.applied.Load() != 0 {
		t.Fatalf("old csrf: %d, applied %d", w.Code, f.applied.Load())
	}
	if w := f.do("POST", waiting, "alice", url.Values{"csrf": {match[1]}, "decision": {"approve"}}); !strings.Contains(w.Body.String(), "Requested restart") || f.applied.Load() != 1 {
		t.Fatalf("approve after restart: applied %d, %s", f.applied.Load(), w.Body)
	}
	// Once decided it is gone from the file, so another restart cannot run it again.
	f.restart(t, path)
	if w := f.do("GET", waiting, "alice", nil); w.Code != http.StatusNotFound || f.applied.Load() != 1 {
		t.Fatalf("approved approval restored: %d, applied %d", w.Code, f.applied.Load())
	}
}

func TestStateFileRejectsExpiredAndForeign(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.json")
	f := newRestartFixture(t, writer())
	if _, err := f.manager.EnableState(path); err != nil {
		t.Fatal(err)
	}
	waiting, _ := f.open(t, "alice")
	f.manager.mu.Lock()
	for _, p := range f.manager.approvals {
		p.expires = time.Now().Add(-time.Second)
	}
	_ = f.manager.saveLocked()
	f.manager.mu.Unlock()
	f.restart(t, path)
	if w := f.do("GET", waiting, "alice", nil); w.Code != http.StatusNotFound {
		t.Fatalf("expired approval restored: %d", w.Code)
	}
	for _, content := range []string{`{"version":2,"link_key":"AAAA"}`, `{"clients":{}}`, `not json`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example", writer(), nil)
		if _, err := m.EnableState(path); err == nil {
			t.Fatalf("accepted state %q", content)
		}
		if after, _ := os.ReadFile(path); string(after) != content {
			t.Fatalf("foreign file overwritten: %q", after)
		}
	}
}

func TestNoticesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path, keyFile := filepath.Join(dir, "approvals.json"), filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, noticeKey, 0600); err != nil {
		t.Fatal(err)
	}
	start := func() *http.ServeMux {
		m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example/", fakeBrowser{}, nil)
		if err := m.EnableInboxNotify(config.InboxNotify{SecretFile: keyFile, Subjects: []string{"alice"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.EnableState(path); err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		m.Routes(mux)
		return mux
	}
	mux := start()
	body := noticeBody(t, time.Now(), "web-pod")
	if w := postNotice(mux, body, sign([]byte(body))); w.Code != http.StatusNoContent {
		t.Fatalf("notice: %d", w.Code)
	}
	page := inboxRequest(start(), "GET", "/approvals", "alice", "", "", nil)
	if !strings.Contains(page.Body.String(), "web-pod") || !strings.Contains(page.Body.String(), "/approve/options/main/apps/web-pod") {
		t.Fatalf("notice not restored: %s", page.Body)
	}
	if other := inboxRequest(start(), "GET", "/approvals", "bob", "", "", nil); strings.Contains(other.Body.String(), "web-pod") {
		t.Fatal("notice restored for another user")
	}
}

// A default path the process may not read must not stop the server.
func TestUnreadableStateFileFallsBackToMemory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads any file")
	}
	path := filepath.Join(t.TempDir(), "approvals.json")
	if err := os.WriteFile(path, []byte(`{}`), 0000); err != nil {
		t.Fatal(err)
	}
	f := newRestartFixture(t, writer())
	key, err := f.manager.EnableState(path)
	if err != nil || len(key) != 32 {
		t.Fatalf("EnableState: %v", err)
	}
	if waiting, _ := f.open(t, "alice"); waiting == "" {
		t.Fatal("approval not created")
	}
	if info, _ := os.Stat(path); info.Size() != 2 {
		t.Fatal("unreadable file was overwritten")
	}
}
