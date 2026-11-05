package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchOutputs(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		bad          bool
	}{
		{"discord", `[{name: discord, type: discord, url_file: /run/discord}]`, false},
		{"custom", `[{name: custom, url: https://example.com, headers: {Authorization: Bearer}, body_template: '{"text":{{json .Text}}}'}]`, false},
		{"unknown type", `[{name: x, type: slack, url: https://example.com}]`, true},
		{"missing url", `[{name: x}]`, true},
		{"both urls", `[{name: x, url: https://example.com, url_file: /run/url}]`, true},
		{"duplicate", `[{name: x, url: https://a.com}, {name: x, url: https://b.com}]`, true},
		{"private", `[{name: x, url: http://10.0.0.1}]`, true},
		{"private allowed", `[{name: x, url: http://10.0.0.1, allow_private_http: true}]`, false},
		{"credentials", `[{name: x, url: 'https://user:password@example.com'}]`, true},
		{"reserved", `[{name: x, url: https://example.com, headers: {Host: elsewhere}}]`, true},
		{"header collision", `[{name: x, url: https://example.com, headers: {Authorization: a}, header_files: {authorization: /run/token}}]`, true},
		{"header injection", `[{name: x, url: https://example.com, headers: {Authorization: "a\r\nb"}}]`, true},
		{"invalid name", `[{name: "secret/url", url: https://example.com}]`, true},
		{"discord template", `[{name: x, type: discord, url_file: /run/url, body_template: '{}'}]`, true},
		{"unknown field", `[{name: x, url_file: /run/url, body: '{}'}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("mode: kubernetes\nwatch:\n  outputs: "+tc.output+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path)
			if err == nil {
				err = c.ValidateWatch()
			}
			if (err != nil) != tc.bad {
				t.Fatalf("err=%v", err)
			}
			if !tc.bad {
				raw, err := c.Effective()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := Load(path); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(raw), "outputs:") {
					t.Fatal("missing outputs")
				}
			}
		})
	}
}
