package config

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ApprovalPush enables payload-less notifications for browser approvals.
type ApprovalPush struct {
	Enabled      bool     `yaml:"enabled"`
	Subject      string   `yaml:"subject"`
	StateFile    string   `yaml:"state_file"`
	AllowedHosts []string `yaml:"allowed_hosts"`
	Language     string   `yaml:"language"`
}

func (p ApprovalPush) validate(web bool) error {
	if p.Language != "en" && p.Language != "ko" {
		return fmt.Errorf("approval.push.language must be en or ko")
	}
	for _, host := range p.AllowedHosts {
		name := strings.TrimPrefix(host, "*.")
		if !validPushHost(name) {
			return fmt.Errorf("approval.push.allowed_hosts must contain DNS names or *.DNS names")
		}
	}
	if !p.Enabled {
		return nil
	}
	if !web {
		return fmt.Errorf("approval.push requires approval.web")
	}
	u, err := url.Parse(p.Subject)
	if err != nil || len(p.Subject) > 1024 || strings.IndexFunc(p.Subject, unicode.IsControl) >= 0 ||
		!(u.Scheme == "mailto" && u.Opaque != "" && strings.Contains(u.Opaque, "@") || u.Scheme == "https" && u.Hostname() != "" && u.User == nil) {
		return fmt.Errorf("approval.push.subject must be a mailto: or https: contact URL")
	}
	if p.StateFile == "" {
		return fmt.Errorf("approval.push.state_file is required without mode defaults")
	}
	return nil
}

func validPushHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
