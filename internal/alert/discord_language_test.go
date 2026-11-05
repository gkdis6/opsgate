package alert

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestDiscordLanguage(t *testing.T) {
	now := time.Now().UTC()
	a := warning("web", "ImagePullBackOff")
	a.Time = now
	a.Summary = "container test: ImagePullBackOff: registry.example/app:missing: not found"
	b := &Batch{SentAt: now, Alerts: []Alert{a}}
	for language, required := range map[string][]string{"ko": {"이미지 다운로드 재시도 대기", "이미지를 찾지 못했습니다", "확인할 사항", a.Reason, a.Summary}, "en": {"Sent:", a.Reason, a.Summary}} {
		sender, err := NewWebhook(WebhookOptions{Format: "discord", Language: language})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := sender.(*webhookSender).body(b)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Embeds []struct{ Description string }
		}
		if json.Unmarshal(raw, &payload) != nil {
			t.Fatal("invalid Discord JSON")
		}
		for _, text := range required {
			if !strings.Contains(payload.Embeds[0].Description, text) {
				t.Errorf("%s missing %q", language, text)
			}
		}
	}
	// Native JSON is source evidence; display language must never rewrite it.
	raw, err := newWebhook(Options{}).(*webhookSender).body(b)
	if err != nil {
		t.Fatal(err)
	}
	var unchanged Batch
	if json.Unmarshal(raw, &unchanged) != nil || unchanged.Alerts[0].Summary != a.Summary || unchanged.Alerts[0].Reason != a.Reason {
		t.Fatal("source alert changed")
	}
	if _, err := NewWebhook(WebhookOptions{Format: "discord", Language: "fr"}); err == nil {
		t.Fatal("unknown language accepted")
	}
	clipped := discordClipLanguage(strings.Repeat("한😀", 3000), 4096, "ko")
	if len(utf16.Encode([]rune(clipped))) > 4096 || !strings.Contains(clipped, "일부 생략") || strings.ContainsRune(clipped, '�') {
		t.Fatal("invalid Korean clipping")
	}
}
