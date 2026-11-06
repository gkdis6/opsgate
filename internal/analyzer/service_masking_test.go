package analyzer

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/alert"
)

func TestReportMaskedBeforeBothNotificationSinks(t *testing.T) {
	c := Default()
	dir := t.TempDir()
	for name, value := range map[string]string{"hmac": "0123456789abcdef", "mcp": "reader-token", "model": "model-key", "discord": "https://discord.com/api/webhooks/123/test-token"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.SecretFile = filepath.Join(dir, "hmac")
	c.MCP.Endpoint, c.MCP.TokenFile = "https://reader.example/mcp", filepath.Join(dir, "mcp")
	c.Model.Name, c.Model.APIKeyFile = "fixture", filepath.Join(dir, "model")
	c.DiscordURLFile = filepath.Join(dir, "discord")
	c.Forward.URL, c.Forward.SecretFile = "https://forward.example/", c.SecretFile
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	sinks := map[string]int{}
	http.DefaultTransport = maskingTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		sinks[r.URL.Host]++
		if strings.Contains(string(body), "notificationcanary") {
			t.Errorf("secret sent to %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"123","channel_id":"456"}`))}, nil
	})
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.notify(context.Background(), `header password="notificationcanary two words"`, []alert.ActionLink{}); err != nil {
		t.Fatal(err)
	}
	if sinks["forward.example"] != 1 || sinks["discord.com"] != 1 {
		t.Fatal(sinks)
	}
}
