package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
						ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: tt.ns, CreationTimestamp: metav1.NewTime(time.Now().Add(-12 * time.Minute))},
						Type:       "Warning", Reason: tt.name, Message: tt.name,
						LastTimestamp:  metav1.NewTime(time.Now().Add(-45 * time.Second)),
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
					// AGE is measured from LastTimestamp (45s), not creation (12m).
					if !strings.Contains(out, "AGE") || !strings.Contains(out, "45s") || strings.Contains(out, "12m") {
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
		{45 * time.Second, "45s"}, {12 * time.Minute, "12m"},
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
