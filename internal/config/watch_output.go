package config

import (
	"fmt"
	"net/http"
	"strings"
)

// WatchOutput delivers JSON directly to a receiver. Each output has its own queue.
type WatchOutput struct {
	Name             string            `yaml:"name"`
	Language         string            `yaml:"language,omitempty"`
	Type             string            `yaml:"type"` // webhook (default) or discord
	URL              string            `yaml:"url,omitempty"`
	URLFile          string            `yaml:"url_file,omitempty"`
	Headers          map[string]string `yaml:"headers,omitempty"`
	HeaderFiles      map[string]string `yaml:"header_files,omitempty"`
	SecretFile       string            `yaml:"secret_file,omitempty"`
	BodyTemplate     string            `yaml:"body_template,omitempty"`
	AllowPrivateHTTP bool              `yaml:"allow_private_http"`
	ProblemsOnly     bool              `yaml:"problems_only"`
}

func (o WatchOutput) validate() error {
	if !watchName(o.Name) {
		return fmt.Errorf("name must have 1 to 64 ASCII letters, digits, '_' or '-'")
	}
	if o.Type != "" && o.Type != "webhook" && o.Type != "discord" {
		return fmt.Errorf("type must be webhook or discord")
	}
	if o.Language != "" && o.Language != "ko" && o.Language != "en" {
		return fmt.Errorf("language must be ko or en")
	}
	if (o.URL == "") == (o.URLFile == "") {
		return fmt.Errorf("set exactly one of url and url_file")
	}
	if o.URL != "" && !validWatchURL(o.URL, o.AllowPrivateHTTP) {
		return fmt.Errorf("url must use https or permitted private/loopback http without credentials or fragments")
	}
	if o.Type == "discord" && o.BodyTemplate != "" {
		return fmt.Errorf("body_template requires type webhook")
	}
	if len(o.BodyTemplate) > 65536 {
		return fmt.Errorf("body_template exceeds 64 KiB")
	}
	seen := map[string]bool{}
	for _, headers := range []map[string]string{o.Headers, o.HeaderFiles} {
		for k, v := range headers {
			key := http.CanonicalHeaderKey(k)
			if !validHeaderName(k) || seen[key] {
				return fmt.Errorf("invalid or duplicate header name")
			}
			switch key {
			case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Trailer", "Upgrade", "X-Opsgate-Signature":
				return fmt.Errorf("reserved header name")
			}
			if v == "" || strings.ContainsAny(v, "\r\n\x00") {
				return fmt.Errorf("header values and file paths must be nonempty without line breaks")
			}
			seen[key] = true
		}
	}
	return nil
}

func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
