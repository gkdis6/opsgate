package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"
	"unicode/utf16"
)

// WebhookOptions configures a named JSON POST destination.
type WebhookOptions struct {
	Options
	Name         string
	Format       string
	Headers      map[string]string
	BodyTemplate string
}

// NewWebhook builds a sender before watching starts. Templates receive Batch
// and a json helper; values from alerts are never evaluated as templates.
func NewWebhook(o WebhookOptions) (Sender, error) {
	if o.Format != "" && o.Format != "webhook" && o.Format != "discord" {
		return nil, fmt.Errorf("unsupported webhook format")
	}
	s := newWebhook(o.Options).(*webhookSender)
	s.name, s.format = o.Name, o.Format
	if s.name == "" {
		s.name = "webhook"
	}
	s.headers = make(map[string]string, len(o.Headers))
	for k, v := range o.Headers {
		s.headers[k] = v
	}
	if o.BodyTemplate != "" {
		if o.Format == "discord" {
			return nil, fmt.Errorf("discord does not accept body_template")
		}
		t, err := template.New("body").Option("missingkey=error").Funcs(template.FuncMap{"json": func(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }}).Parse(o.BodyTemplate)
		if err != nil {
			return nil, fmt.Errorf("invalid body_template syntax")
		}
		s.template = t
	}
	return s, nil
}

type limitedBody struct{ bytes.Buffer }

func (b *limitedBody) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("body exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}

func (s *webhookSender) body(b *Batch) ([]byte, error) {
	if s.template != nil {
		var out limitedBody
		if err := s.template.Execute(&out, b); err != nil {
			return nil, fmt.Errorf("body_template execution failed")
		}
		if !json.Valid(out.Bytes()) {
			return nil, fmt.Errorf("body_template must render valid JSON")
		}
		return out.Bytes(), nil
	}
	if s.format == "discord" {
		var text strings.Builder
		fmt.Fprintf(&text, "Sent: %s\n", b.SentAt.UTC().Format(time.RFC3339))
		for _, a := range b.Alerts {
			fmt.Fprintf(&text, "\n%s [%s] %s %s %s/%s: %s\n%s\n", a.Time.UTC().Format(time.RFC3339), a.Severity, a.Cluster, a.Kind, a.Namespace, a.Name, a.Reason, a.Summary)
			if a.Suppressed > 0 {
				fmt.Fprintf(&text, "(+%d similar)\n", a.Suppressed)
			}
			if a.Hint != "" {
				fmt.Fprintf(&text, "Hint: %s\n", a.Hint)
			}
			if a.Restart != nil {
				fmt.Fprintf(&text, "Restart suggestion: %s %s/%s %s\n", a.Restart.Kind, a.Restart.Namespace, a.Restart.Name, a.Restart.URL)
			}
		}
		if b.Dropped > 0 {
			fmt.Fprintf(&text, "\n%d alerts dropped\n", b.Dropped)
		}
		description := discordClip(text.String(), 4096)
		return json.Marshal(map[string]any{"allowed_mentions": map[string]any{"parse": []string{}}, "embeds": []any{map[string]any{"title": "opsgate watch", "description": description}}})
	}
	return json.Marshal(b)
}

// Discord counts UTF-16 units; keep a visible marker when a batch is too long.
func discordClip(s string, limit int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= limit {
		return s
	}
	const marker = "\n… truncated; reduce batch size or inspect cluster via MCP"
	n := limit - len(utf16.Encode([]rune(marker)))
	if units[n-1] >= 0xD800 && units[n-1] <= 0xDBFF {
		n--
	}
	return string(utf16.Decode(units[:n])) + marker
}
