package kube

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/alert"
)

type watchCaptureTransport struct{ body string }

func (c *watchCaptureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	b, err := io.ReadAll(r.Body)
	c.body = string(b)
	return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, err
}

func TestWatchMasksAllTextBeforeDeliveryAndKeys(t *testing.T) {
	w := testWatcher()
	var sent alert.Alert
	w.emit = func(a alert.Alert) { sent = a }
	w.send([]alert.Alert{{Severity: alert.Warning, Kind: "Pod", Namespace: "apps", Name: "web", Reason: `password="reason-canary"`, Summary: "password=summary-canary", Hint: "password=hint-canary", Key: "password=key-canary", Display: &alert.Display{Reason: "password=display-canary"}, Restart: &alert.Workload{Kind: "deployment", Namespace: "apps", Name: "web", URL: "https://ops.example/?password=url-canary"}}})
	for _, template := range []string{"", `{"alert":{{json .Alerts}}}`} {
		capture := &watchCaptureTransport{}
		sender, err := alert.NewWebhook(alert.WebhookOptions{Options: alert.Options{URL: "https://receiver.example/", Client: &http.Client{Transport: capture}}, BodyTemplate: template})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sender.Send(context.Background(), &alert.Batch{Alerts: []alert.Alert{sent}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(capture.body, "canary") || strings.Contains(sent.Key, "canary") {
			t.Fatalf("secret in output/key: %s / %s", capture.body, sent.Key)
		}
	}
	w.send([]alert.Alert{{Severity: alert.Warning, Reason: "OOMKilled", Summary: strings.Repeat("한", 400), Hint: strings.Repeat("x", 400)}})
	if sent.Reason != "OOMKilled" || utf8.RuneCountInString(sent.Hint) > 301 || utf8.RuneCountInString(sent.Summary) > 301 {
		t.Fatalf("bad machine reason or text bound: %+v", sent)
	}
}
