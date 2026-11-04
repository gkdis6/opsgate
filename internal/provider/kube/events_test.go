package kube

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestEvents(t *testing.T) {
	now := time.Now()
	var objects []runtime.Object
	for _, tt := range []struct {
		name, ns, typ, kind string
		ago                 time.Duration
	}{
		{"newest", "public", "Warning", "Pod", time.Minute}, {"older", "public", "Warning", "Pod", 2 * time.Minute},
		{"stale", "public", "Warning", "Pod", 2 * time.Hour}, {"private-event", "private", "Warning", "Pod", time.Minute},
		{"normal", "public", "Normal", "Deployment", time.Minute},
	} {
		objects = append(objects, &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: tt.ns}, Type: tt.typ, Reason: tt.name, Message: "untrusted\nmessage\twith control", LastTimestamp: metav1.NewTime(now.Add(-tt.ago)), InvolvedObject: corev1.ObjectReference{Name: "app", Namespace: tt.ns, Kind: tt.kind}})
	}
	for _, tt := range []struct {
		name         string
		in           eventsInput
		want, absent string
	}{
		{"default", eventsInput{}, "newest", "normal"},
		{"limit", eventsInput{Limit: 1}, "truncated to 1", "older"},
		{"all", eventsInput{WarningsOnly: boolPtr(false)}, "normal", "stale"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(objects...)
			out, err := p.events(context.Background(), tool.Caller{}, tt.in)
			if err != nil || !strings.Contains(out, tt.want) || strings.Contains(out, tt.absent) || strings.Contains(out, "private-event") {
				t.Fatal(out, err)
			}
			if strings.Contains(out, "untrusted\n") {
				t.Fatal("unescaped event message")
			}
		})
	}
	p := testProvider(objects...)
	out, err := p.recentEvents(context.Background(), "public", false, "1h", 50, "app", "Pod")
	if err != nil || strings.Contains(out, "normal") || strings.Index(out, "newest") > strings.Index(out, "older") {
		t.Fatal(out, err)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestEventsScanBudgetAndNewestRetention(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		t.Run(fmt.Sprint(eligible), func(t *testing.T) {
			p := testProvider()
			calls, scanned := 0, 0
			now := time.Now()
			p.client.(*fake.Clientset).PrependReactor("list", "events", func(action ktesting.Action) (bool, runtime.Object, error) {
				calls++
				opts := action.(ktesting.ListActionImpl).GetListOptions()
				if opts.Limit != 500 {
					t.Errorf("page limit = %d, want 500", opts.Limit)
				}
				if calls > 10 {
					return true, nil, fmt.Errorf("scanned beyond 5000 events")
				}
				page := &corev1.EventList{ListMeta: metav1.ListMeta{Continue: fmt.Sprint(calls)}}
				for i := 0; i < 500; i++ {
					scanned++
					ns := "public"
					if !eligible {
						ns = "private"
					}
					page.Items = append(page.Items, corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Annotations: map[string]string{"discard": "metadata"}}, Type: "Warning", Reason: fmt.Sprintf("event-%04d", scanned), Message: "message", LastTimestamp: metav1.NewTime(now.Add(time.Duration(scanned-5001) * time.Second))})
				}
				return true, page, nil
			})
			out, err := p.events(context.Background(), tool.Caller{}, eventsInput{Limit: 500, Since: "2h"})
			if err != nil || calls != 10 || !strings.Contains(out, "truncated") {
				t.Fatalf("calls=%d: %s, %v", calls, out, err)
			}
			if eligible && (!strings.Contains(out, "event-5000") || strings.Contains(out, "event-4500") || strings.Index(out, "event-5000") > strings.Index(out, "event-4999")) {
				t.Fatalf("did not retain newest 500 events: %s", out)
			}
			if !eligible && strings.Contains(out, "event-") {
				t.Fatal("denied events leaked")
			}
		})
	}
}
func TestDurationAndLimits(t *testing.T) {
	for _, tt := range []struct {
		value string
		valid bool
	}{{"", true}, {"1h", true}, {"168h", true}, {"168h1s", false}, {"7d", false}, {"0s", false}, {"-1h", false}, {"garbage", false}} {
		_, err := duration(tt.value, "1h")
		if (err == nil) != tt.valid {
			t.Errorf("%q: %v", tt.value, err)
		}
	}
	for _, tt := range []struct{ n, want int }{{0, 100}, {-1, 1}, {501, 500}, {20, 20}} {
		if got := clamp(tt.n, 100, 500); got != tt.want {
			t.Fatal(got)
		}
	}
}

func TestClusterEventsPolicy(t *testing.T) {
	for _, policy := range []namespacePolicy{
		{allow: []string{"public"}},
		{allow: []string{"*"}, deny: []string{"private"}},
	} {
		for _, kind := range []string{"Node", "PersistentVolume", "Namespace"} {
			t.Run(kind+strings.Join(policy.allow, ","), func(t *testing.T) {
				objects := []runtime.Object{
					&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "public"}},
					&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "public"}},
					&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "public"}},
				}
				for _, tt := range []struct{ name, ns, involvedNS string }{
					{"visible", "public", ""},
					{"denied-event", "private", ""},
					{"denied-reference", "public", "private"},
				} {
					objects = append(objects, &corev1.Event{
						ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: tt.ns, CreationTimestamp: metav1.NewTime(time.Now().Add(-50 * time.Minute))},
						Type:       "Warning", Reason: tt.name, Message: tt.name,
						LastTimestamp:  metav1.NewTime(time.Now().Add(-30 * time.Minute)),
						InvolvedObject: corev1.ObjectReference{Name: "public", Kind: kind, Namespace: tt.involvedNS},
					})
				}
				p := testProvider(objects...)
				p.policy = policy
				for _, entry := range []string{"get", "overview", "events"} {
					var out string
					var err error
					switch entry {
					case "get":
						out, err = p.get(context.Background(), tool.Caller{}, getInput{Kind: kind, Name: "public"})
					case "overview":
						out, err = p.overview(context.Background(), tool.Caller{}, struct{}{})
					case "events":
						out, err = p.events(context.Background(), tool.Caller{}, eventsInput{})
					}
					if err != nil || !strings.Contains(out, "visible") || strings.Contains(out, "denied-event") || strings.Contains(out, "denied-reference") || strings.Contains(out, "private") {
						t.Fatalf("%s: %s %v", entry, out, err)
					}
					// AGE is measured from LastTimestamp (30m), not creation (50m).
					if !strings.Contains(out, "AGE") || !strings.Contains(out, "30m") || strings.Contains(out, "50m") {
						t.Fatalf("%s: missing event age: %s", entry, out)
					}
				}
			})
		}
	}
}

func TestAge(t *testing.T) {
	for _, tt := range []struct {
		ago  time.Duration
		want string
	}{
		{12 * time.Minute, "12m"},
		{3*time.Hour + 12*time.Minute, "3h12m"}, {95 * 24 * time.Hour, "95d"},
		{(2*365 + 30) * 24 * time.Hour, "2y30d"}, {-time.Hour, "<invalid>"},
	} {
		if got := age(time.Now().Add(-tt.ago).UTC().Format(time.RFC3339)); got != tt.want {
			t.Errorf("%v: got %q, want %q", tt.ago, got, tt.want)
		}
	}
	if got := age("invalid"); got != "-" {
		t.Fatal(got)
	}
	p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "old", Namespace: "public", CreationTimestamp: metav1.NewTime(time.Now().Add(-95 * 24 * time.Hour))}})
	out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pods"})
	if err != nil || !strings.Contains(out, "95d") {
		t.Fatal(out, err)
	}
}
