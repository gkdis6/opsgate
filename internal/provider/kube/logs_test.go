package kube

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestLogsRequest(t *testing.T) {
	for _, tt := range []struct {
		name       string
		tail       int
		grep, want string
	}{{"defaults", 0, "ERROR", "ERROR failure"}, {"clamp", 999, "", "ok\nERROR failure"}} {
		t.Run(tt.name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"metadata":{"name":"app","namespace":"public"},"spec":{"containers":[{"name":"web"}]}}`
				if strings.HasSuffix(r.URL.Path, "/log") {
					q := r.URL.Query()
					expected := "200"
					if tt.tail > 500 {
						expected = "500"
					}
					if q.Get("tailLines") != expected || q.Get("limitBytes") != "1048576" || q.Get("sinceSeconds") != "60" || q.Get("container") != "web" || q.Get("previous") != "true" {
						t.Error(q)
					}
					body = "ok\nERROR failure\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			client, err := kubernetes.NewForConfig(&rest.Config{Host: "https://cluster.invalid", Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			p := testProvider()
			p.client = client
			out, err := p.logs(context.Background(), tool.Caller{}, logsInput{Namespace: "public", Pod: "app", Tail: tt.tail, Since: "1m", Previous: true, Grep: tt.grep})
			if err != nil || out != tt.want {
				t.Fatal(out, err)
			}
		})
	}
}

func TestLogsValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		in     logsInput
		want   string
		denied bool
	}{
		{"deny", logsInput{Namespace: "private", Pod: "app"}, "", true},
		{"invalid pod", logsInput{Namespace: "public", Pod: "../app"}, "invalid name", false},
		{"invalid since", logsInput{Namespace: "public", Pod: "app", Since: "169h"}, "since must", false},
		{"multiple", logsInput{Namespace: "public", Pod: "app"}, "web, sidecar", false},
		{"unknown", logsInput{Namespace: "public", Pod: "app", Container: "missing"}, "unknown container", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "public"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}, {Name: "sidecar"}}}})
			_, err := p.logs(context.Background(), tool.Caller{}, tt.in)
			if err == nil || tt.denied && !errors.Is(err, tool.ErrDenied) || !strings.Contains(err.Error(), tt.want) {
				t.Fatal(err)
			}
		})
	}
}
