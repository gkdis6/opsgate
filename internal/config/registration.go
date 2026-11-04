package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// validateRegistrationPolicy checks administrator-controlled DCR settings.
func (c Config) validateRegistrationPolicy() error {
	b := c.Auth.Builtin
	switch b.UnknownRedirectHosts {
	case "", "deny", "approve":
	default:
		return fmt.Errorf("auth.builtin.unknown_redirect_hosts must be deny or approve")
	}
	if b.UnknownRedirectHosts == "approve" && len(b.Admins) == 0 {
		return fmt.Errorf("auth.builtin.unknown_redirect_hosts approve requires at least one admin")
	}
	for _, admin := range b.Admins {
		if admin == "" || len(admin) > 512 || strings.ContainsFunc(admin, unicode.IsControl) || strings.TrimSpace(admin) != admin {
			return fmt.Errorf("auth.builtin.admins contains an invalid subject")
		}
		found := false
		for _, user := range b.Users {
			if admin == user.Subject || !strings.HasPrefix(admin, "oidc:") && !strings.HasPrefix(user.Subject, "oidc:") && strings.EqualFold(admin, user.Subject) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("auth.builtin.admins subjects must be allowed users")
		}
	}
	scheme := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:$`)
	for _, host := range b.AllowedRedirectHosts {
		if len(host) == 0 || len(host) > 253 {
			return fmt.Errorf("auth.builtin.allowed_redirect_hosts contains an invalid host")
		}
		if scheme.MatchString(host) {
			name := strings.ToLower(strings.TrimSuffix(host, ":"))
			if name == "cursor" || strings.Contains(name, ".") {
				continue
			}
			return fmt.Errorf("auth.builtin.allowed_redirect_hosts contains an unsupported private scheme")
		}
		if net.ParseIP(host) != nil || strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && net.ParseIP(host[1:len(host)-1]) != nil {
			continue
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return fmt.Errorf("auth.builtin.allowed_redirect_hosts requires exact hosts or private schemes")
			}
			for _, ch := range label {
				if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
					return fmt.Errorf("auth.builtin.allowed_redirect_hosts requires exact hosts or private schemes")
				}
			}
		}
	}
	if b.NotifyURL != "" {
		u, err := url.Parse(b.NotifyURL)
		if err != nil || len(b.NotifyURL) > 4096 || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.Contains(b.NotifyURL, "#") ||
			(u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(strings.ToLower(u.Hostname())))) {
			return fmt.Errorf("auth.builtin.notify_url must use https or loopback http without credentials or fragments")
		}
	}
	return nil
}
