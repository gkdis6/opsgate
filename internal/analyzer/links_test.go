package analyzer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeutralizeLinks(t *testing.T) {
	const origin = "https://approve.example"
	for _, tc := range []struct{ name, in, want string }{
		{"bare link", "see https://attacker.example/x?d=note text", "see hxxps://attacker[.]example/x?d=note text"},
		{"plain http and case", "HTTP://a.b.example:8080/p", "hxxp://a[.]b[.]example:8080/p"},
		{"markdown link", "[details](https://attacker.example/?d=secret)", "details (hxxps://attacker[.]example/?d=secret)"},
		{"angle markdown link", "[x](<https://attacker.example/a>)", "x (hxxps://attacker[.]example/a)"},
		{"autolink", "<https://attacker.example/a>", "<hxxps://attacker[.]example/a>"},
		{"approval link kept", "[재시작 승인](https://approve.example/approve/action/abc?x=1)", "[재시작 승인](https://approve.example/approve/action/abc?x=1)"},
		{"approval bare kept", "https://APPROVE.example/approve/x", "https://APPROVE.example/approve/x"},
		{"approval over http defanged", "http://approve.example/approve/x", "hxxp://approve[.]example/approve/x"},
		{"approval with userinfo defanged", "https://u@approve.example/x", "hxxps://u@approve[.]example/x"},
		{"inline code kept", "run `curl https://api.example/v1` now", "run `curl https://api.example/v1` now"},
		{"unmatched backtick", "a ` https://attacker.example/q", "a ` hxxps://attacker[.]example/q"},
		{"fenced block kept", "```sh\ncurl https://api.example/v1\n```\nhttps://attacker.example/", "```sh\ncurl https://api.example/v1\n```\nhxxps://attacker[.]example/"},
		{"tilde fence kept", "~~~\nhttps://api.example\n~~~", "~~~\nhttps://api.example\n~~~"},
		{"no scheme untouched", "attacker.example/?d=1", "attacker.example/?d=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := neutralizeLinks(tc.in, origin); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
	if got := neutralizeLinks("https://approve.example/x", ""); got != "hxxps://approve[.]example/x" {
		t.Fatalf("without an approval origin every link is defanged, got %q", got)
	}
}

func TestDiscordReportSuppressesPreviewsAndDefangsLinks(t *testing.T) {
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
	c.Report.ApprovalURL = "https://approve.example"
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	bodies := map[string]string{}
	http.DefaultTransport = maskingTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		bodies[r.URL.Host] = string(body)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"123","channel_id":"456"}`))}, nil
	})
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	text := "원인 [근거](https://attacker.example/?d=note) 승인 [재시작](https://approve.example/approve/action/a)"
	if err := s.notify(context.Background(), text, nil); err != nil {
		t.Fatal(err)
	}
	var discord struct {
		Content string `json:"content"`
		Flags   int    `json:"flags"`
	}
	if err := json.Unmarshal([]byte(bodies["discord.com"]), &discord); err != nil {
		t.Fatalf("discord body %q: %v", bodies["discord.com"], err)
	}
	if discord.Flags&4 == 0 {
		t.Fatalf("discord message must set SUPPRESS_EMBEDS, flags=%d", discord.Flags)
	}
	for host, body := range bodies {
		if strings.Contains(body, "attacker.example") {
			t.Errorf("%s received a live attacker link: %s", host, body)
		}
		if !strings.Contains(body, "approve.example/approve/action/a") {
			t.Errorf("%s lost the approval link: %s", host, body)
		}
	}
	if !strings.Contains(discord.Content, "hxxps://attacker[.]example/?d=note") {
		t.Fatalf("defanged link missing: %q", discord.Content)
	}
}
