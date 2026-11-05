package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

func watchOutputSender(o config.WatchOutput) (alert.Sender, error) {
	url := o.URL
	if o.URLFile != "" {
		b, err := os.ReadFile(o.URLFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read url_file")
		}
		url = strings.TrimSpace(string(b))
	}
	if err := o.ValidateURL(url); err != nil {
		return nil, err
	}
	headers := map[string]string{}
	for k, v := range o.Headers {
		headers[http.CanonicalHeaderKey(k)] = v
	}
	for k, path := range o.HeaderFiles {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read header_files entry")
		}
		headers[http.CanonicalHeaderKey(k)] = strings.TrimSpace(string(b))
	}
	for _, v := range headers {
		if v == "" {
			return nil, fmt.Errorf("empty header value")
		}
		for _, c := range []byte(v) {
			if c != '\t' && (c < 32 || c == 127) {
				return nil, fmt.Errorf("invalid header value")
			}
		}
	}
	var secret []byte
	if o.SecretFile != "" {
		b, err := os.ReadFile(o.SecretFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read secret_file")
		}
		secret = bytes.TrimSpace(b)
		if len(secret) < 16 {
			return nil, fmt.Errorf("secret_file must hold at least 16 bytes")
		}
	}
	return alert.NewWebhook(alert.WebhookOptions{Options: alert.Options{URL: url, Secret: secret, PrivateOnly: strings.HasPrefix(strings.ToLower(url), "http:")}, Name: o.Name, Format: o.Type, Headers: headers, BodyTemplate: o.BodyTemplate})
}
