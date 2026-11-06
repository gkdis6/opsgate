package analyzer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
)

func TestReceiverFieldLimitsAndProjection(t *testing.T) {
	for _, field := range []string{"reason", "summary", "hint", "cluster", "kind", "namespace", "name", "restart-url"} {
		t.Run(field, func(t *testing.T) {
			s := newService(Default(), []byte("0123456789abcdef"))
			b := testBatch(time.Now())
			a := &b.Alerts[0]
			long := strings.Repeat("x", 5000)
			switch field {
			case "reason":
				a.Reason = long
			case "summary":
				a.Summary = long
			case "hint":
				a.Hint = long
			case "cluster":
				a.Cluster = long
			case "kind":
				a.Kind = long
			case "namespace":
				a.Namespace = long
			case "name":
				a.Name = long
			case "restart-url":
				a.Restart = &alert.Workload{Kind: "deployment", Namespace: "apps", Name: "web", URL: long}
			}
			if got := post(s, b, true); got != 413 || len(s.jobs) != 0 || len(s.seen) != 0 {
				t.Fatalf("oversized field accepted: %d", got)
			}
		})
	}
	s := newService(Default(), []byte("0123456789abcdef"))
	b := testBatch(time.Now())
	b.Text = strings.Repeat("unused", 100)
	b.Language = "unused"
	b.Actions = []alert.ActionLink{{URL: "unused"}}
	b.Alerts[0].Display = &alert.Display{Summary: "unused"}
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	for _, j := range s.jobs {
		raw, _ := json.Marshal(j.batch)
		if strings.Contains(string(raw), "unused") {
			t.Fatal("unused fields retained")
		}
	}
}

func TestReceiverByteBudgetsAtomicReplacementAndRelease(t *testing.T) {
	now := time.Now()
	s := newService(Default(), []byte("0123456789abcdef"))
	s.now = func() time.Time { return now }
	s.jobByteLimit = 2000
	s.queueByteLimit = 4000
	b := testBatch(now)
	b.Alerts[0].Hint = strings.Repeat("h", 100)
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	before := s.queuedBytes
	b.SentAt = now.Add(time.Second)
	b.Alerts[0].Hint = strings.Repeat("한", 300)
	if got := post(s, b, true); got != 202 || s.queuedBytes <= before {
		t.Fatalf("replacement not charged: %d, %d -> %d", got, before, s.queuedBytes)
	}
	before = s.queuedBytes
	b.SentAt = now.Add(2 * time.Second)
	b.Alerts[0].Reason = "different"
	b.Alerts[0].Summary = "different"
	if got := post(s, b, true); got != 503 || s.queuedBytes != before {
		t.Fatalf("job byte budget ignored: %d", got)
	}
	// One new job fits, but two new jobs must fail atomically on the global cap.
	b.Alerts[0].Name = "second"
	other := b.Alerts[0]
	other.Name = "third"
	b.Alerts = append(b.Alerts, other)
	if got := post(s, b, true); got != 503 || len(s.jobs) != 1 || s.queuedBytes != before {
		t.Fatalf("global budget/atomicity: %d, jobs=%d", got, len(s.jobs))
	}
	b.Alerts = b.Alerts[:1]
	b.SentAt = now.Add(3 * time.Second)
	if got := post(s, b, true); got != 202 {
		t.Fatal(got)
	}
	// A larger duplicate must be rejected before replacing the stored job.
	s.queueByteLimit = s.queuedBytes + 1
	b.SentAt = now.Add(4 * time.Second)
	b.Alerts[0].Hint += "😀"
	before = s.queuedBytes
	if got := post(s, b, true); got != 503 || s.queuedBytes != before {
		t.Fatalf("global replacement budget: %d", got)
	}
	now = now.Add(s.cfg.BatchWindow)
	for {
		if _, ok := s.take(); !ok {
			break
		}
	}
	if s.queuedBytes != 0 {
		t.Fatalf("retained accounting after dequeue: %d", s.queuedBytes)
	}
}
