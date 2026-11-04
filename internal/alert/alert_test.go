package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTest(t *testing.T, opts Options) (*Dispatcher, *time.Time) {
	t.Helper()
	if opts.Batch == 0 {
		opts.Batch = time.Hour
	}
	if opts.MaxAlerts == 0 {
		opts.MaxAlerts = 50
	}
	d := New(opts)
	d.backoff = []time.Duration{0, 0, 0}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	return d, &now
}

func warning(name, reason string) Alert {
	return Alert{Cluster: "main", Severity: Warning, Kind: "Pod", Namespace: "apps", Name: name, Reason: reason, Summary: "container app restarted"}
}

func TestPrepareCooldownAndCoalescing(t *testing.T) {
	d, now := newTest(t, Options{Cooldown: 15 * time.Minute})
	b := d.prepare([]Alert{warning("a", "OOMKilled"), warning("a", "OOMKilled"), warning("b", "OOMKilled")})
	if len(b.Alerts) != 2 || b.Alerts[0].Suppressed != 1 || b.Alerts[1].Suppressed != 0 {
		t.Fatalf("burst not coalesced: %+v", b.Alerts)
	}
	if !strings.Contains(b.Text, "[warning] main Pod apps/a: container app restarted (+1 similar)") {
		t.Fatalf("text = %q", b.Text)
	}
	*now = now.Add(5 * time.Minute)
	if b := d.prepare([]Alert{warning("a", "OOMKilled"), warning("a", "OOMKilled")}); b != nil {
		t.Fatalf("cooldown ignored: %+v", b)
	}
	*now = now.Add(11 * time.Minute)
	b = d.prepare([]Alert{warning("a", "OOMKilled")})
	if len(b.Alerts) != 1 || b.Alerts[0].Suppressed != 2 {
		t.Fatalf("suppressed repeats not reported: %+v", b.Alerts)
	}
	// Quiet keys are forgotten once cooled down.
	*now = now.Add(16 * time.Minute)
	d.prepare(nil)
	if len(d.last) != 0 || len(d.suppressed) != 0 {
		t.Fatalf("keys retained: %v %v", d.last, d.suppressed)
	}
}

func TestPrepareInfoKeepsLatestAndSkipsCooldown(t *testing.T) {
	d, now := newTest(t, Options{Cooldown: time.Hour})
	scaled := func(summary string) Alert {
		return Alert{Cluster: "main", Severity: Info, Kind: "Deployment", Namespace: "apps", Name: "web", Reason: "Scaled", Summary: summary}
	}
	b := d.prepare([]Alert{scaled("replicas 1 → 2"), scaled("replicas 2 → 3")})
	if len(b.Alerts) != 1 || b.Alerts[0].Summary != "replicas 2 → 3" || b.Alerts[0].Suppressed != 1 {
		t.Fatalf("info not coalesced: %+v", b.Alerts)
	}
	*now = now.Add(time.Minute)
	if b := d.prepare([]Alert{scaled("replicas 3 → 1")}); b == nil || len(b.Alerts) != 1 {
		t.Fatal("info alert held by cooldown")
	}
}

func TestPrepareLimitsAndDrops(t *testing.T) {
	d, _ := newTest(t, Options{MaxAlerts: 2})
	for range queueSize + 3 {
		d.Emit(warning("x", "Error"))
	}
	b := d.prepare([]Alert{warning("a", "Error"), warning("b", "Error"), warning("c", "Error")})
	if len(b.Alerts) != 2 || b.Dropped != 4 || !strings.Contains(b.Text, "4 alerts dropped") {
		t.Fatalf("limit/drop: %d alerts, dropped %d, %q", len(b.Alerts), b.Dropped, b.Text)
	}
	if b := d.prepare(nil); b != nil {
		t.Fatalf("dropped count not reset: %+v", b)
	}
}

func TestPostSignsAndDoesNotFollowRedirects(t *testing.T) {
	secret := []byte("0123456789abcdef")
	var mu sync.Mutex
	var bodies [][]byte
	var redirected bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			redirected = true
			return
		}
		body, _ := io.ReadAll(r.Body)
		if got := r.Header.Get("X-Opsgate-Signature"); got != Sign(secret, body) {
			t.Errorf("signature %q does not verify", got)
		}
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()

	d, _ := newTest(t, Options{URL: srv.URL + "/hook?key=s3cret", Secret: secret, Client: srv.Client()})
	b := d.prepare([]Alert{warning("a", "OOMKilled")})
	if err := d.post(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	var got Batch
	if err := json.Unmarshal(bodies[0], &got); err != nil || got.Source != "opsgate" || len(got.Alerts) != 1 || got.Alerts[0].Reason != "OOMKilled" {
		t.Fatalf("body = %s (%v)", bodies[0], err)
	}
	if strings.Contains(string(bodies[0]), `"Key"`) {
		t.Fatal("internal key serialized")
	}

	d.opts.Sender.(*webhookSender).opts.URL = srv.URL + "/redirect"
	if err := d.post(context.Background(), b); err == nil || redirected {
		t.Fatalf("redirect followed: err=%v redirected=%v", err, redirected)
	}
}

func TestPostRetriesServerErrorsOnly(t *testing.T) {
	for _, tc := range []struct {
		status, calls int
	}{{http.StatusServiceUnavailable, 3}, {http.StatusBadRequest, 1}} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(tc.status)
		}))
		d, _ := newTest(t, Options{URL: srv.URL, Client: srv.Client()})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := d.post(ctx, d.prepare([]Alert{warning("a", "Error")}))
		cancel()
		srv.Close()
		if err == nil || calls != tc.calls {
			t.Fatalf("status %d: err=%v calls=%d", tc.status, err, calls)
		}
	}
}

func TestPostErrorHidesURL(t *testing.T) {
	d, _ := newTest(t, Options{URL: "https://127.0.0.1:1/hook?key=s3cret"})
	_, err := d.opts.Sender.Send(context.Background(), &Batch{})
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFlushesOnShutdown(t *testing.T) {
	got := make(chan Batch, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b Batch
		json.NewDecoder(r.Body).Decode(&b)
		got <- b
	}))
	defer srv.Close()
	d, _ := newTest(t, Options{URL: srv.URL, Client: srv.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.Run(ctx)
		close(done)
	}()
	d.Emit(warning("a", "Error"))
	cancel()
	<-done
	select {
	case b := <-got:
		if len(b.Alerts) != 1 {
			t.Fatalf("%+v", b)
		}
	default:
		t.Fatal("pending alert not flushed")
	}
}

func TestPrivateOnlyRefusesPublicAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	d, _ := newTest(t, Options{URL: srv.URL, PrivateOnly: true})
	if _, err := d.opts.Sender.Send(context.Background(), &Batch{}); err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	d.opts.Sender.(*webhookSender).opts.URL = "http://192.0.2.1:9/hook"
	if _, err := d.opts.Sender.Send(context.Background(), &Batch{}); err == nil || !strings.Contains(err.Error(), "not private") {
		t.Fatalf("public address allowed: %v", err)
	}
}

func TestRenderIncludesAdvice(t *testing.T) {
	a := Alert{Cluster: "main", Severity: Warning, Kind: "Pod", Namespace: "apps", Name: "web-1", Reason: "Error", Summary: "container app restarted",
		Hint: "Check the logs.", Restart: &Workload{Kind: "deployment", Namespace: "apps", Name: "web"}}
	b := &Batch{Alerts: []Alert{a}}
	want := "[warning] main Pod apps/web-1: container app restarted\n  hint: Check the logs.\n  if needed, ask to restart: deployment apps/web"
	if got := render(b); got != want {
		t.Fatalf("text = %q", got)
	}
	if subject := emailSubject(b); strings.Contains(subject, "hint") || strings.Contains(subject, "restart:") {
		t.Fatalf("subject carries advice: %q", subject)
	}
	b.Alerts[0].Restart.URL = "https://ops.example/approve/restart/main/deployment/apps/web"
	if got := render(b); got != want+"\n  or approve it here: "+b.Alerts[0].Restart.URL {
		t.Fatalf("text with link = %q", got)
	}
}
