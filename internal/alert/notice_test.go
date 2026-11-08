package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type noticeTransport func(*http.Request) (*http.Response, error)

func (f noticeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoticeBatchSigningRetryAndFiltering(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	var previous string
	sender := NewNoticeSender(Options{URL: "https://ops.example/approvals/notify", Secret: []byte("0123456789abcdef"), Client: &http.Client{Transport: noticeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("X-Opsgate-Signature") != Sign([]byte("0123456789abcdef"), b) {
			t.Fatal("bad signature")
		}
		if calls > 1 && previous != string(b) {
			t.Fatal("retry changed body")
		}
		previous = string(b)
		for _, secret := range []string{"DO-NOT-SEND", "summary", "hint", "options_url", "text", "restart", "display"} {
			if strings.Contains(string(b), secret) {
				t.Fatal("unexpected notice field", string(b))
			}
		}
		var batch NoticeBatch
		if json.Unmarshal(b, &batch) != nil || len(batch.Notices) != 2 || !batch.SentAt.Equal(now) {
			t.Fatal("batch", string(b))
		}
		if batch.Notices[0].WorkloadKind != "deployment" || batch.Notices[0].WorkloadName != "web" || batch.Notices[0].Pod != "pod-a" {
			t.Fatal("identifiers", string(b))
		}
		code := http.StatusNoContent
		if calls == 1 {
			code = 429
		}
		if calls == 2 {
			code = 503
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}})
	d := New(Options{Sender: sender, Batch: time.Second, MaxAlerts: 50, ChoicesOnly: true, ProblemsOnly: true})
	d.now = func() time.Time { return now }
	d.backoff = []time.Duration{0, 0, 0}
	a := Alert{Time: now, Cluster: "main", Namespace: "apps", Name: "pod-a", Kind: "Pod", Severity: Warning, Reason: "CrashLoopBackOff", Summary: "DO-NOT-SEND", Hint: "DO-NOT-SEND", OptionsURL: "https://ops.example/choices", Workload: &Workload{Kind: "deployment", Name: "web"}}
	// Filter before queuing so a burst of other events cannot crowd out choices.
	for range queueSize + 1 {
		info := a
		info.Severity = Info
		d.Emit(info)
		plain := a
		plain.OptionsURL = ""
		d.Emit(plain)
	}
	d.Emit(a)
	a.Name = "pod-b"
	d.Emit(a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
	if calls != 3 {
		t.Fatal("retry count", calls)
	}
	// Sender also defends against informational and unlinked alerts.
	calls = 0
	a.Severity = Info
	if retry, err := sender.Send(context.Background(), &Batch{SentAt: now, Alerts: []Alert{a}}); err != nil || retry || calls != 0 {
		t.Fatal("info sent")
	}
	a.Severity = Warning
	a.OptionsURL = ""
	sender.Send(context.Background(), &Batch{SentAt: now, Alerts: []Alert{a}})
	if calls != 0 {
		t.Fatal("unlinked alert sent")
	}
}

func TestNoticeIdentifierBounds(t *testing.T) {
	n := Notice{Cluster: strings.Repeat("c", 64), Namespace: strings.Repeat("n", 63), Pod: strings.Repeat("p", 253), WorkloadKind: "deployment", WorkloadName: strings.Repeat("w", 253), Reason: strings.Repeat("r", 128), Severity: Warning, OccurredAt: time.Now()}
	if !n.Valid() {
		t.Fatal("valid bounded notice")
	}
	batch := NoticeBatch{SentAt: time.Now()}
	for range 50 {
		batch.Notices = append(batch.Notices, n)
	}
	b, err := json.Marshal(batch)
	if err != nil || len(b) > 64<<10 {
		t.Fatal("watch batch exceeds endpoint limit", len(b), err)
	}
	n.Pod += "p"
	if n.Valid() {
		t.Fatal("oversized identifier accepted")
	}
}
