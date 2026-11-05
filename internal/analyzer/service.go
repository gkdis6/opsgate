package analyzer

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
)

// New loads credentials without sending network requests. No watcher or model is
// started unless all files and endpoints are valid.
func New(c Config) (*Service, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	key, err := credential(c.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("secret_file: %w", err)
	}
	if len(key) < 16 {
		return nil, fmt.Errorf("secret_file requires at least 16 bytes")
	}
	token, err := credential(c.MCP.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("mcp.token_file: %w", err)
	}
	model, err := newModel(c)
	if err != nil {
		return nil, err
	}
	discord, err := credential(c.DiscordURLFile)
	if err != nil {
		return nil, fmt.Errorf("discord_url_file: %w", err)
	}
	// Use the same URL policy as watch, with no plain HTTP for remote receivers.
	u, parseErr := url.Parse(discord)
	if parseErr != nil || u.Scheme != "https" || u.Host != "discord.com" || u.User != nil || u.RawQuery != "" || strings.Contains(discord, "#") || !strings.HasPrefix(u.Path, "/api/webhooks/") || len(strings.Split(strings.TrimPrefix(u.Path, "/api/webhooks/"), "/")) != 2 {
		return nil, fmt.Errorf("discord_url_file must hold a Discord incoming webhook URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/api/webhooks/"), "/")
	if parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid Discord webhook path")
	}
	sender, err := alert.NewWebhook(alert.WebhookOptions{Options: alert.Options{URL: discord + "?wait=true&with_components=true", Client: &http.Client{Transport: discordReceiptTransport{base: http.DefaultTransport}}}, BodyTemplate: `{"content":{{json .Text}},"allowed_mentions":{"parse":[]}{{if .Actions}},"components":[{"type":1,"components":[{{range $i,$a:=.Actions}}{{if $i}},{{end}}{"type":2,"style":5,"label":{{json $a.Label}},"url":{{json $a.URL}}}{{end}}]}]{{end}}}`})
	if err != nil {
		return nil, fmt.Errorf("cannot configure notifications")
	}
	s := newService(c, []byte(key))
	e := &engine{cfg: c, model: model, dial: func(ctx context.Context) (toolSession, error) { return connect(ctx, c, token) }}
	s.investigate = e.investigate
	s.ready = func(ctx context.Context) error {
		session, err := e.dial(ctx)
		if err != nil {
			return fmt.Errorf("MCP reader unavailable")
		}
		defer session.Close()
		tools, err := session.List(ctx)
		if err != nil || len(tools) == 0 {
			return fmt.Errorf("MCP read tools unavailable")
		}
		return nil
	}
	var forward alert.Sender
	if c.Forward.URL != "" {
		if err := (config.WatchOutput{AllowPrivateHTTP: c.Forward.AllowPrivateHTTP}).ValidateURL(c.Forward.URL); err != nil {
			return nil, fmt.Errorf("forward.url: %w", err)
		}
		key, err := credential(c.Forward.SecretFile)
		if err != nil || len(key) < 16 {
			return nil, fmt.Errorf("forward.secret_file must hold at least 16 bytes")
		}
		forward, err = alert.NewWebhook(alert.WebhookOptions{Options: alert.Options{URL: c.Forward.URL, Secret: []byte(key), PrivateOnly: strings.HasPrefix(c.Forward.URL, "http:")}})
		if err != nil {
			return nil, err
		}
	}
	s.notify = func(ctx context.Context, text string, actions []alert.ActionLink) error {
		// Forward independently before Discord delivery so a Discord outage cannot suppress Telegram.
		var forwardErr error
		if forward != nil {
			_, forwardErr = forward.Send(ctx, &alert.Batch{Source: "opsgate-analyzer", SentAt: time.Now().UTC(), Language: c.Report.Language, Text: text, Actions: actions})
		}
		for i, part := range chunks(redact.String(text), 1900) {
			var last error
			for attempt, wait := range []time.Duration{0, time.Second, 4 * time.Second} {
				if wait > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(wait):
					}
				}
				batch := &alert.Batch{Text: part}
				if i == 0 {
					batch.Actions = actions
				}
				retry, err := sender.Send(ctx, batch)
				last = err
				if err == nil || !retry || attempt == 2 {
					break
				}
			}
			if last != nil {
				return fmt.Errorf("Discord notification failed")
			}
		}
		return forwardErr
	}
	return s, nil
}
