package approval

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
)

// approveWithCheck approves a restart whose plan carries check and returns
// the approval page path.
func approveWithCheck(t *testing.T, limit time.Duration, check tool.Check) (*restartFixture, string) {
	t.Helper()
	f := newRestartFixture(t, writer())
	t.Cleanup(f.manager.Close)
	f.manager.verifyEvery, f.manager.verifyFor = 5*time.Millisecond, limit
	f.check.Store(check)
	path, csrf := f.open(t, "alice")
	w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}})
	if w.Code != http.StatusOK || f.applied.Load() != 1 {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "checking recovery") || !strings.Contains(w.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("approval page does not say it is checking recovery: %s", w.Body)
	}
	return f, path
}

func waitRecovery(t *testing.T, f *restartFixture, path string) recovery {
	t.Helper()
	id := strings.TrimPrefix(path, "/approve/")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.manager.mu.Lock()
		r := f.manager.approvals[id].recovery
		f.manager.mu.Unlock()
		if r.state != recoveryChecking {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("recovery check did not finish")
	return recovery{}
}

func TestRecoveryReportedAfterTwoHealthyObservations(t *testing.T) {
	var calls atomic.Int32
	f, path := approveWithCheck(t, 2*time.Second, func(context.Context) (tool.Health, error) {
		if calls.Add(1) == 1 {
			return tool.Health{Detail: "available 0/1, CrashLoopBackOff"}, nil
		}
		return tool.Health{Done: true, Healthy: true, Detail: "available 1/1"}, nil
	})
	r := waitRecovery(t, f, path)
	if r.state != recoveryRecovered || r.detail != "available 1/1" || calls.Load() != 3 {
		t.Fatalf("recovery %+v after %d checks", r, calls.Load())
	}
	page := f.do("GET", path, "alice", nil)
	if !strings.Contains(page.Body.String(), "Recovered / 복구됨") || strings.Contains(page.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("page: %s", page.Body)
	}
	inbox := f.do("GET", "/approvals", "alice", nil)
	if !strings.Contains(inbox.Body.String(), "Recovered / 복구됨 (available 1/1)") {
		t.Fatalf("inbox: %s", inbox.Body)
	}
	if next := f.do("GET", "/approvals/next", "alice", nil); next.Header().Get("Location") != path {
		t.Fatalf("next goes to %q, want the finished check %q", next.Header().Get("Location"), path)
	}
	var latest struct{ Body string }
	if err := json.Unmarshal(f.do("GET", "/approvals/latest", "alice", nil).Body.Bytes(), &latest); err != nil || !strings.Contains(latest.Body, "recovered") {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if strings.Contains(latest.Body, "apps") || strings.Contains(latest.Body, "web") {
		t.Fatalf("notification text names the target: %q", latest.Body)
	}
}

func TestRecoveryFailureAndUnreadableTarget(t *testing.T) {
	f, path := approveWithCheck(t, 2*time.Second, func(context.Context) (tool.Health, error) {
		return tool.Health{Done: true, Detail: "rollout exceeded its progress deadline, ImagePullBackOff"}, nil
	})
	if r := waitRecovery(t, f, path); r.state != recoveryFailing || !strings.Contains(r.detail, "ImagePullBackOff") {
		t.Fatalf("failure: %+v", r)
	}
	page := f.do("GET", path, "alice", nil).Body.String()
	if !strings.Contains(page, "Not recovered yet / 아직 복구되지 않음") || !strings.Contains(page, "choose another action") {
		t.Fatalf("page: %s", page)
	}

	f, path = approveWithCheck(t, 30*time.Millisecond, func(context.Context) (tool.Health, error) {
		return tool.Health{}, errors.New("forbidden: secret detail")
	})
	r := waitRecovery(t, f, path)
	if r.state != recoveryUnverified || strings.Contains(r.detail, "secret") {
		t.Fatalf("unreadable target: %+v", r)
	}
}

func TestRecoveryStillProgressingAtDeadlineIsNotRecovered(t *testing.T) {
	f, path := approveWithCheck(t, 30*time.Millisecond, func(context.Context) (tool.Health, error) {
		return tool.Health{Detail: "available 0/2"}, nil
	})
	if r := waitRecovery(t, f, path); r.state != recoveryFailing || r.detail != "available 0/2" {
		t.Fatalf("deadline: %+v", r)
	}
}

func TestNoCheckKeepsAppliedStatus(t *testing.T) {
	f := newRestartFixture(t, writer())
	path, csrf := f.open(t, "alice")
	w := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}})
	if !strings.Contains(w.Body.String(), "Applied / 적용됨") || strings.Contains(w.Body.String(), "recovery") {
		t.Fatalf("page: %s", w.Body)
	}
}

func TestOpenOnPhoneQR(t *testing.T) {
	f := newRestartFixture(t, writer())
	path, csrf := f.open(t, "alice")
	page := f.do("GET", path, "alice", nil).Body.String()
	if !strings.Contains(page, "Open on your phone / 폰에서 열기") || !strings.Contains(page, "<svg ") {
		t.Fatalf("pending approval has no QR: %s", page)
	}
	inbox := f.do("GET", "/approvals", "alice", nil).Body.String()
	if !strings.Contains(inbox, "<svg ") {
		t.Fatalf("inbox has no QR: %s", inbox)
	}
	done := f.do("POST", path, "alice", url.Values{"csrf": {csrf}, "decision": {"approve"}}).Body.String()
	if strings.Contains(done, "<svg ") {
		t.Fatal("decided approval still offers a QR")
	}
	// A plain-http origin cannot be opened from a phone.
	f.manager.publicURL = "http://127.0.0.1:8787"
	if strings.Contains(f.do("GET", "/approvals", "alice", nil).Body.String(), "<svg ") {
		t.Fatal("QR for a loopback origin")
	}
}
