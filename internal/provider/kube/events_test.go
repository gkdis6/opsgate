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
