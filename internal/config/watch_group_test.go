package config

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func groupConfig(t *testing.T) Config {
	t.Helper()
	c := Default()
	c.Watch.Outputs = []WatchOutput{{Name: "hook", URL: "https://example.com"}}
	c.Watch.Email = WatchEmail{Host: "smtp.example.com", Port: 587, From: "alerts@example.com", To: []string{"me@example.com"}, Batch: time.Minute}
	c.Watch.Inbox = WatchInbox{URL: "https://ops.example.com", SecretFile: "/etc/inbox-key"}
	c.Watch.Groups = []WatchGroup{{Name: "customer", Namespaces: []string{"bid", "chagok", "shop-*"}, Outputs: []string{"hook", "inbox"}, ProblemsOnly: true, Batch: time.Second}, {Name: "rest", Namespaces: []string{"*"}, Outputs: []string{"email"}, Batch: 15 * time.Minute}}
	return c
}

func TestWatchGroupValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"absent", func(c *Config) { c.Watch.Groups = nil }, ""},
		{"empty", func(c *Config) { c.Watch.Groups = []WatchGroup{} }, "1 to 50"},
		{"too many", func(c *Config) {
			for range 50 {
				c.Watch.Groups = append(c.Watch.Groups, c.Watch.Groups[0])
			}
		}, "1 to 50"},
		{"missing name", func(c *Config) { c.Watch.Groups[0].Name = "" }, "name must"},
		{"duplicate name", func(c *Config) { c.Watch.Groups[1].Name = "customer" }, "name must"},
		{"name injection", func(c *Config) { c.Watch.Groups[0].Name = "customer\r\nBcc: other@example.com" }, "name must"},
		{"mention name", func(c *Config) { c.Watch.Groups[0].Name = "@everyone" }, "name must"},
		{"long name", func(c *Config) { c.Watch.Groups[0].Name = strings.Repeat("a", 65) }, "name must"},
		{"bad glob", func(c *Config) { c.Watch.Groups[0].Namespaces = []string{"["} }, "invalid glob"},
		{"empty namespaces", func(c *Config) { c.Watch.Groups[0].Namespaces = nil }, "namespaces"},
		{"namespace-less", func(c *Config) { c.Watch.Groups[0].Namespaces = []string{""} }, ""},
		{"many patterns", func(c *Config) { c.Watch.Groups[0].Namespaces = make([]string, 101) }, "namespaces"},
		{"long pattern", func(c *Config) { c.Watch.Groups[0].Namespaces = []string{strings.Repeat("a", 254)} }, "invalid glob"},
		{"no outputs", func(c *Config) { c.Watch.Groups[0].Outputs = nil }, "outputs"},
		{"duplicate outputs", func(c *Config) { c.Watch.Groups[0].Outputs = []string{"hook", "hook"} }, "duplicate output"},
		{"unknown output", func(c *Config) { c.Watch.Groups[0].Outputs = []string{"typo"} }, "unknown"},
		{"disabled email", func(c *Config) { c.Watch.Email.Host = "" }, "disabled"},
		{"disabled inbox", func(c *Config) { c.Watch.Inbox = WatchInbox{} }, "disabled"},
		{"email name collision", func(c *Config) { c.Watch.Outputs[0].Name = "email" }, "reserves"},
		{"inbox name collision", func(c *Config) { c.Watch.Outputs[0].Name = "inbox" }, "reserves"},
		{"missing batch", func(c *Config) { c.Watch.Groups[0].Batch = 0 }, "batch"},
		{"negative batch", func(c *Config) { c.Watch.Groups[0].Batch = -time.Second }, "batch"},
		{"webhook too quick", func(c *Config) { c.Watch.Groups[0].Batch = time.Millisecond }, "batch"},
		{"webhook too slow", func(c *Config) { c.Watch.Groups[0].Batch = 11 * time.Minute }, "batch"},
		{"webhook upper bound", func(c *Config) { c.Watch.Groups[0].Batch = 10 * time.Minute }, ""},
		{"email too quick", func(c *Config) { c.Watch.Groups[1].Batch = time.Second }, "batch"},
		{"email too slow", func(c *Config) { c.Watch.Groups[1].Batch = 61 * time.Minute }, "batch"},
		{"email upper bound", func(c *Config) { c.Watch.Groups[1].Batch = time.Hour }, ""},
		{"mixed valid", func(c *Config) {
			c.Watch.Groups[0].Outputs = append(c.Watch.Groups[0].Outputs, "email")
			c.Watch.Groups[0].Batch = time.Minute
		}, ""},
		{"mixed invalid", func(c *Config) { c.Watch.Groups[0].Outputs = append(c.Watch.Groups[0].Outputs, "email") }, "batch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := groupConfig(t)
			tc.change(&c)
			err := c.Validate()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWatchGroupConfigShowRoundTrip(t *testing.T) {
	c := groupConfig(t)
	effective, err := c.Effective()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(effective, "effective")
	if err != nil {
		t.Fatalf("%v\n%s", err, effective)
	}
	if !reflect.DeepEqual(c.Watch.Groups, got.Watch.Groups) {
		t.Fatalf("groups changed: %+v / %+v", c.Watch.Groups, got.Watch.Groups)
	}
	again, err := got.Effective()
	if err != nil || !bytes.Equal(effective, again) {
		t.Fatalf("effective config changed: %v", err)
	}
	if !strings.Contains(string(effective), "batch: 15m0s") {
		t.Fatalf("duration not readable: %s", effective)
	}
	_, err = Parse([]byte("watch:\n  groups: []\n"), "empty")
	if err == nil || !strings.Contains(err.Error(), "1 to 50") {
		t.Fatal(err)
	}
	_, err = Parse([]byte("watch:\n  groups:\n    - name: x\n      namespces: ['*']\n"), "typo")
	if err == nil || !strings.Contains(err.Error(), "namespces") {
		t.Fatal(err)
	}
	c = Default()
	effective, err = c.Effective()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(effective), "groups:") {
		t.Fatal("absent groups emitted")
	}
}
