package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
)

func testBatch(now time.Time) alert.Batch {
	return alert.Batch{Source: "opsgate", SentAt: now, Alerts: []alert.Alert{{Time: now, Cluster: "main", Kind: "Pod", Namespace: "apps", Name: "web", Severity: alert.Warning, Reason: "OOMKilled", Summary: "container app restarted"}}}
}
func post(s *Service, b alert.Batch, signed bool) int {
	raw, _ := json.Marshal(b)
	r := httptest.NewRequest("POST", "/alerts", bytes.NewReader(raw))
	if signed {
		r.Header.Set("X-Opsgate-Signature", alert.Sign(s.secret, raw))
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Code
}
func TestSignedReceiver(t *testing.T) {
	now := time.Now().UTC()
	s := newService(Default(), []byte("0123456789abcdef"))
	s.now = func() time.Time { return now }
	b := testBatch(now)
	if got := post(s, b, false); got != 401 {
		t.Fatal(got)
	}
	if len(s.jobs) != 0 {
		t.Fatal("unsigned batch queued")
	}
	for _, at := range []time.Time{now.Add(-11 * time.Minute), now.Add(2 * time.Minute)} {
		bad := b
		bad.SentAt = at
		if got := post(s, bad, true); got != 400 {
			t.Fatal(got)
		}
	}
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	if len(s.jobs) != 1 || s.jobs["main/Pod/apps/web"].batch.Alerts[0].Suppressed != 0 {
		t.Fatal("replay queued")
	}
	b.SentAt = now.Add(time.Second)
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	if s.jobs["main/Pod/apps/web"].batch.Alerts[0].Suppressed != 1 {
		t.Fatal("alerts not merged")
	}
	if _, ok := s.take(); ok {
		t.Fatal("batch window skipped")
	}
	now = now.Add(s.cfg.BatchWindow)
	if _, ok := s.take(); !ok {
		t.Fatal("batch not ready")
	}
	b.SentAt = now
	if got := post(s, b, true); got != 202 || len(s.jobs) != 0 {
		t.Fatal("cooldown ignored")
	}
}
func TestReceiverQueueAtomicityAndInfo(t *testing.T) {
	c := Default()
	c.QueueSize = 1
	now := time.Now().UTC()
	s := newService(c, []byte("0123456789abcdef"))
	s.now = func() time.Time { return now }
	b := testBatch(now)
	other := b.Alerts[0]
	other.Name = "other"
	b.Alerts = append(b.Alerts, other)
	if got := post(s, b, true); got != 503 || len(s.jobs) != 0 || len(s.seen) != 0 {
		t.Fatal("partially queued full batch")
	}
	b.Alerts = b.Alerts[:1]
	b.Alerts[0].Severity = alert.Info
	if got := post(s, b, true); got != 202 || len(s.jobs) != 0 {
		t.Fatal("info triggered analysis")
	}
	b.Alerts[0].Severity = alert.Warning
	if got := post(s, b, true); got != 202 || len(s.jobs) != 1 {
		t.Fatal("warning not queued")
	}
}
func TestReceiverBodyLimitsAndTamper(t *testing.T) {
	s := newService(Default(), []byte("0123456789abcdef"))
	for _, body := range []string{`{}`, strings.Repeat("x", 1<<20+1)} {
		r := httptest.NewRequest("POST", "/alerts", strings.NewReader(body))
		r.Header.Set("X-Opsgate-Signature", alert.Sign(s.secret, []byte(body)))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 && w.Code != 413 {
			t.Fatal(w.Code)
		}
	}
	raw, _ := json.Marshal(testBatch(time.Now()))
	r := httptest.NewRequest("POST", "/alerts", strings.NewReader(string(raw)+" "))
	r.Header.Set("X-Opsgate-Signature", alert.Sign(s.secret, raw))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}
func TestFailureNotification(t *testing.T) {
	s := newService(Default(), []byte("0123456789abcdef"))
	sent := ""
	s.investigate = func(context.Context, alert.Batch) (investigation, error) {
		return investigation{}, context.DeadlineExceeded
	}
	s.notify = func(_ context.Context, text string, _ []alert.ActionLink) error { sent = text; return nil }
	s.process(context.Background(), testBatch(time.Now()))
	if !strings.Contains(sent, "분석 실패") {
		t.Fatal(sent)
	}
	if !strings.Contains(sent, "원인은 확정되지") {
		t.Fatal(sent)
	}
}
