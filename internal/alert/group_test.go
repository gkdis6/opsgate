package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestGroupNamespaceMatching(t *testing.T) {
	for _, tc := range []struct {
		pattern, namespace string
		want               bool
	}{
		{"shop-*", "shop-one", true}, {"shop-*", "my-shop-one", false},
		{"[ab]id", "bid", true}, {"*", "", true}, {"", "", true},
		{"**", "", false}, {"?*", "", false}, {"", "apps", false},
		{"apps", "apps/other", false}, {"*", "apps/other", false},
		{"[", "apps", false},
	} {
		if got := matchesNamespace([]string{tc.pattern}, tc.namespace); got != tc.want {
			t.Errorf("%q / %q = %v", tc.pattern, tc.namespace, got)
		}
	}
}

func TestGroupOrderFiltersAndUnmatched(t *testing.T) {
	all, _ := newTest(t, Options{Sender: testSender{name: "all"}})
	problems, _ := newTest(t, Options{ProblemsOnly: true, Sender: testSender{name: "problems"}})
	r := NewRouter([]Group{
		{Name: "first", Namespaces: []string{"shop-*"}, Outputs: []string{"all", "problems"}, ProblemsOnly: true, Batch: time.Second},
		{Name: "second", Namespaces: []string{"shop-*", "apps"}, Outputs: []string{"all", "problems"}, Batch: time.Minute},
	}, []*Dispatcher{all, problems})
	for _, ns := range []string{"shop-one", "apps", "unmatched", ""} {
		for _, severity := range []string{Warning, Info} {
			a := warning(ns, "Error")
			a.Namespace, a.Severity = ns, severity
			r.Emit(a)
		}
	}
	for i, want := range []int{1, 1, 2, 1} {
		if got := len(r.dispatchers[i].queue); got != want {
			t.Fatalf("queue %d has %d, want %d", i, got, want)
		}
	}
	b := r.unmatched.prepare(nil)
	if b == nil || b.Dropped != 4 || len(b.Alerts) != 0 || b.Text != "4 alerts dropped" {
		t.Fatalf("unmatched: %+v", b)
	}
	if r.unmatched.prepare(nil) != nil {
		t.Fatal("dropped counted twice")
	}
}

func TestGroupBatchTimingAndCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := make(chan *Batch, 10)
		output := New(Options{Sender: testSender{"hook", func(_ context.Context, b *Batch) (bool, error) { sent <- b; return false, nil }}, Batch: time.Hour, Cooldown: time.Hour, MaxAlerts: 1})
		r := NewRouter([]Group{
			{Name: "fast", Namespaces: []string{"fast"}, Outputs: []string{"hook"}, Batch: time.Second},
			{Name: "slow", Namespaces: []string{"slow"}, Outputs: []string{"hook"}, Batch: time.Minute},
		}, []*Dispatcher{output})
		ctx, cancel := context.WithCancel(context.Background())
		go r.Run(ctx)
		a := warning("one", "Error")
		a.Key = "same-key" // Even identical keys cannot cross group cooldowns.
		a.Namespace = "fast"
		r.Emit(a)
		a.Namespace = "slow"
		r.Emit(a)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if b := <-sent; b.Group != "fast" || len(b.Alerts) != 1 {
			t.Fatalf("fast: %+v", b)
		}
		select {
		case b := <-sent:
			t.Fatalf("early slow batch: %+v", b)
		default:
		}
		a.Namespace = "fast"
		r.Emit(a)
		synctest.Wait()
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if b := <-sent; b.Group != "slow" || len(b.Alerts) != 1 {
			t.Fatalf("slow: %+v", b)
		}
		select {
		case b := <-sent:
			t.Fatalf("cooldown ignored: %+v", b)
		default:
		}
		a.Namespace = "slow"
		a.Key = "new-one"
		r.Emit(a)
		a.Key = "new-two"
		r.Emit(a)
		cancel()
		synctest.Wait()
		b := <-sent
		if b.Group != "slow" || len(b.Alerts) != 1 || b.Dropped != 1 {
			t.Fatalf("bounded shutdown batch: %+v", b)
		}
	})
}

func TestGroupFailureIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		sent := make(chan *Batch, 1)
		output := New(Options{Sender: testSender{"hook", func(_ context.Context, b *Batch) (bool, error) {
			if b.Group == "slow" {
				<-release
				return false, errors.New("unavailable")
			}
			sent <- b
			return false, nil
		}}, Batch: time.Hour, MaxAlerts: 10})
		r := NewRouter([]Group{
			{Name: "slow", Namespaces: []string{"slow"}, Outputs: []string{"hook"}, Batch: time.Second},
			{Name: "fast", Namespaces: []string{"fast"}, Outputs: []string{"hook"}, Batch: time.Second},
		}, []*Dispatcher{output})
		ctx, cancel := context.WithCancel(context.Background())
		go r.Run(ctx)
		for _, ns := range []string{"slow", "fast"} {
			a := warning("one", "Error")
			a.Namespace = ns
			r.Emit(a)
		}
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case b := <-sent:
			if b.Group != "fast" {
				t.Fatal(b)
			}
		default:
			t.Fatal("blocked group held healthy group")
		}
		close(release)
		cancel()
		synctest.Wait()
	})
}

func TestGroupNoGroupsCompatibility(t *testing.T) {
	d, _ := newTest(t, Options{ProblemsOnly: true})
	r := NewRouter(nil, []*Dispatcher{d})
	if len(r.dispatchers) != 1 || r.dispatchers[0] != d || r.unmatched != nil {
		t.Fatal("legacy dispatcher changed")
	}
	r.Emit(warning("a", "Error"))
	r.Emit(Alert{Severity: Info})
	if len(d.queue) != 1 {
		t.Fatal("legacy filtering changed")
	}
	b := d.prepare([]Alert{<-d.queue})
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), `"group"`) {
		t.Fatal("legacy payload changed")
	}
}

func TestGroupFormats(t *testing.T) {
	b := &Batch{Group: "customer", Alerts: []Alert{warning("one", "Error")}}
	sender, _ := NewWebhook(WebhookOptions{Format: "discord"})
	raw, err := sender.(*webhookSender).body(b)
	if err != nil || !bytes.Contains(raw, []byte(`"title":"opsgate watch · customer"`)) {
		t.Fatalf("%s %v", raw, err)
	}
	sender, _ = NewWebhook(WebhookOptions{BodyTemplate: `{"group":{{json .Group}}}`})
	raw, err = sender.(*webhookSender).body(b)
	if err != nil || string(raw) != `{"group":"customer"}` {
		t.Fatalf("%s %v", raw, err)
	}
	sender, _ = NewWebhook(WebhookOptions{})
	raw, err = sender.(*webhookSender).body(b)
	if err != nil || !bytes.Contains(raw, []byte(`"group":"customer"`)) {
		t.Fatalf("%s %v", raw, err)
	}
	if !strings.HasPrefix(emailSubject(b), "[customer] [opsgate]") {
		t.Fatal(emailSubject(b))
	}
}

func TestGroupUnmatchedSummary(t *testing.T) {
	var out bytes.Buffer
	old := log.Writer()
	log.SetOutput(&out)
	defer log.SetOutput(old)
	r := NewRouter([]Group{{Name: "apps", Namespaces: []string{"apps"}, Outputs: []string{"hook"}, Batch: time.Minute}}, []*Dispatcher{New(Options{Sender: testSender{name: "hook"}, Batch: time.Minute})})
	r.Emit(Alert{Namespace: "secret-namespace", Summary: "secret-summary"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Run(ctx)
	if got := out.String(); !strings.Contains(got, "1 alerts dropped (no matching group)") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
}

func TestGroupCooldownMemoryAndCounters(t *testing.T) {
	d, _ := newTest(t, Options{Group: "bounded", Cooldown: time.Hour, MaxAlerts: 1})
	for i := range queueSize + 10 {
		a := warning(time.Unix(int64(i), 0).String(), "Error")
		d.prepare([]Alert{a})
	}
	if len(d.last) > queueSize || len(d.suppressed) > queueSize {
		t.Fatal("unbounded cooldown state")
	}
	d.dropped = int(^uint(0) >> 1)
	b := d.prepare([]Alert{warning("extra1", "Error"), warning("extra2", "Error")})
	if b.Dropped != int(^uint(0)>>1) || increment(b.Dropped) != b.Dropped {
		t.Fatal("counter overflow")
	}
}

func TestGroupInboxRestrictions(t *testing.T) {
	inbox := New(Options{Sender: testSender{name: "inbox"}, Batch: time.Second, ChoicesOnly: true, ProblemsOnly: true, MaxAlerts: 50})
	r := NewRouter([]Group{{Name: "apps", Namespaces: []string{"*"}, Outputs: []string{"inbox"}, Batch: time.Minute}}, []*Dispatcher{inbox})
	a := warning("one", "Error")
	r.Emit(a) // A group cannot give a pod without a choices link access to inbox.
	a.OptionsURL = "https://ops.example/approve/options/main/apps/one"
	a.Severity = Info
	r.Emit(a)
	a.Severity = Warning
	a.Kind = "Node"
	r.Emit(a)
	a.Kind = "Pod"
	r.Emit(a)
	d := r.routes[0].outputs[0]
	if len(d.queue) != 1 || d.opts.Batch != time.Minute || !d.opts.ChoicesOnly || !d.opts.ProblemsOnly || d.opts.MaxAlerts != 50 {
		t.Fatalf("inbox restrictions lost: %+v, queue=%d", d.opts, len(d.queue))
	}
}
