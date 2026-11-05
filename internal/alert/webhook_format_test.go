package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestCustomWebhookWire(t *testing.T) {
	secret := []byte("0123456789abcdef")
	summary := "quote \" newline\n한글 {{.Source}}"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Opsgate-Signature") != Sign(secret, raw) {
			t.Errorf("invalid request headers")
		}
		var got struct {
			Content string
			Alerts  []Alert
		}
		if err := json.Unmarshal(raw, &got); err != nil || got.Content != summary || len(got.Alerts) != 1 {
			t.Errorf("body=%s err=%v", raw, err)
		}
	}))
	defer srv.Close()
	s, err := NewWebhook(WebhookOptions{Options: Options{URL: srv.URL, Secret: secret}, Name: "custom", Headers: map[string]string{"Authorization": "Bearer test"}, BodyTemplate: `{"content":{{json (index .Alerts 0).Summary}},"alerts":{{json .Alerts}}}`})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "custom" {
		t.Fatal(s.Name())
	}
	if _, err := s.Send(context.Background(), &Batch{Alerts: []Alert{{Summary: summary}}}); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateFailuresDoNotPostOrExposeData(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	for _, body := range []string{`{{`, `{{.Missing}}`, `{"text":"{{(index .Alerts 0).Summary}}"}`, `{{range .Alerts}}{{.Summary}}{{end}}`} {
		s, err := NewWebhook(WebhookOptions{Options: Options{URL: srv.URL}, BodyTemplate: body})
		if err == nil {
			retry, sendErr := s.Send(context.Background(), &Batch{Alerts: []Alert{{Summary: strings.Repeat("secret\"", 200000)}}})
			if sendErr == nil || retry || strings.Contains(sendErr.Error(), "secret") {
				t.Fatalf("retry=%v err=%v", retry, sendErr)
			}
		}
	}
	if calls != 0 {
		t.Fatal("invalid payload posted")
	}
}

func TestDiscordPayload(t *testing.T) {
	s, err := NewWebhook(WebhookOptions{Format: "discord"})
	if err != nil {
		t.Fatal(err)
	}
	a := warning("web-1", "OOMKilled")
	a.Time = time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC)
	a.Summary = "@everyone memory exceeded"
	a.Hint = "Check limits"
	a.Restart = &Workload{Kind: "deployment", Namespace: "apps", Name: "web", URL: "https://ops.example/approve/restart/main/deployment/apps/web"}
	raw, err := s.(*webhookSender).body(&Batch{SentAt: a.Time, Alerts: []Alert{a}, Dropped: 2})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Embeds          []struct{ Description string }
		AllowedMentions struct{ Parse []string } `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.AllowedMentions.Parse) != 0 {
		t.Fatal("mentions enabled")
	}
	for _, value := range []string{"2026-11-05T01:02:03Z", "main", "Pod", "apps/web-1", "OOMKilled", a.Summary, a.Hint, a.Restart.URL, "2 alerts dropped"} {
		if !strings.Contains(body.Embeds[0].Description, value) {
			t.Errorf("missing %q", value)
		}
	}
	clipped := discordClip(strings.Repeat("한😀", 3000), 4096)
	if len(utf16.Encode([]rune(clipped))) > 4096 || !strings.Contains(clipped, "truncated") || strings.ContainsRune(clipped, '�') {
		t.Fatal("invalid truncation")
	}
}

func TestWebhookRetries429(t *testing.T) {
	for _, status := range []int{204, 400, 429, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		s, err := NewWebhook(WebhookOptions{Options: Options{URL: srv.URL}})
		if err != nil {
			t.Fatal(err)
		}
		retry, err := s.Send(context.Background(), &Batch{})
		srv.Close()
		if retry != (status == 429 || status >= 500) || (err != nil) != (status >= 300) {
			t.Fatalf("status %d retry=%v err=%v", status, retry, err)
		}
	}
}
