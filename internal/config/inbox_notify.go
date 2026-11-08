package config

import (
	"fmt"
	"net/url"
)

// WatchInbox is an independent signed notice output, enabled by URL.
type WatchInbox struct {
	URL              string `yaml:"url"`
	SecretFile       string `yaml:"secret_file"`
	AllowPrivateHTTP bool   `yaml:"allow_private_http"`
}

// InboxNotify names the configured users who receive watch notices.
type InboxNotify struct {
	SecretFile string   `yaml:"secret_file"`
	Subjects   []string `yaml:"subjects"`
}

func (w WatchInbox) validate() error {
	if w.URL == "" && w.SecretFile == "" && !w.AllowPrivateHTTP {
		return nil
	}
	u, err := url.Parse(w.URL)
	if err != nil || !validWatchURL(w.URL, w.AllowPrivateHTTP) || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || w.SecretFile == "" {
		return fmt.Errorf("watch.inbox requires an https or permitted private http origin and secret_file")
	}
	return nil
}

func (c Config) validateInboxNotify() error {
	n := c.Approval.InboxNotify
	if n.SecretFile == "" && len(n.Subjects) == 0 {
		return nil
	}
	if n.SecretFile == "" || len(n.Subjects) == 0 || len(n.Subjects) > 100 || !c.Approval.Web || c.Auth.Mode != AuthBuiltin {
		return fmt.Errorf("approval.inbox_notify requires secret_file, 1 to 100 subjects and builtin web approval")
	}
	known := map[string]bool{}
	for _, u := range c.Auth.Builtin.Users {
		known[u.Subject] = true
	}
	seen := map[string]bool{}
	for _, s := range n.Subjects {
		if !known[s] || s == "" || len(s) > 1024 || seen[s] {
			return fmt.Errorf("approval.inbox_notify.subjects must be unique configured users")
		}
		seen[s] = true
	}
	return nil
}
