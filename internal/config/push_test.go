package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestApprovalPushDefaultsAndRoundTrip(t *testing.T) {
	for _, mode := range []string{"", "kubernetes"} {
		text := "approval:\n  web: true\n  push:\n    enabled: true\n    subject: mailto:owner@example.com\n"
		if mode == "" {
			text += "    state_file: /data/push.json\n"
		} else {
			text = "mode: " + mode + "\n" + text
		}
		cfg, err := Parse([]byte(text), "test")
		if err != nil {
			t.Fatal(err)
		}
		p := cfg.Approval.Push
		if !p.Enabled || p.Language != "en" || len(p.AllowedHosts) != 4 || p.StateFile == "" {
			t.Fatal("missing push defaults", p)
		}
		if mode != "" && p.StateFile != "/var/lib/opsgate/push.json" {
			t.Fatal("missing mode state default")
		}
		effective, err := cfg.Effective()
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(effective, "effective")
		if err != nil || !reflect.DeepEqual(again.Approval.Push, p) {
			t.Fatalf("push round trip failed: %v", err)
		}
	}
	if Default().Approval.Push.Enabled {
		t.Fatal("push must be opt-in")
	}
}

func TestApprovalPushValidation(t *testing.T) {
	base := "approval:\n  web: true\n  push:\n    enabled: true\n    subject: mailto:owner@example.com\n    state_file: /data/push.json\n"
	for _, tc := range []struct{ name, input string }{
		{"requires web", strings.Replace(base, "web: true", "web: false", 1)},
		{"requires subject", strings.Replace(base, "subject: mailto:owner@example.com", "subject: ''", 1)},
		{"requires state", strings.Replace(base, "state_file: /data/push.json", "state_file: ''", 1)},
		{"no http subject", strings.Replace(base, "mailto:owner@example.com", "http://example.com", 1)},
		{"no arbitrary subject", strings.Replace(base, "mailto:owner@example.com", "owner@example.com", 1)},
		{"no empty https subject", strings.Replace(base, "mailto:owner@example.com", "https:", 1)},
		{"no credentials", strings.Replace(base, "mailto:owner@example.com", "https://user:secret@example.com", 1)},
		{"no wildcard all", base + "    allowed_hosts: ['*']\n"},
		{"no wildcard middle", base + "    allowed_hosts: ['push.*.example.com']\n"},
		{"no URL hosts", base + "    allowed_hosts: ['https://fcm.googleapis.com']\n"},
		{"language", base + "    language: fr\n"},
		{"empty language", base + "    language: ''\n"},
		{"state collision", base + "auth:\n  builtin:\n    state_file: /data/./push.json\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.input), "test"); err == nil {
				t.Fatal("invalid push configuration accepted")
			}
		})
	}
	for _, contact := range []string{"mailto:owner@example.com", "https://example.com/contact"} {
		if _, err := Parse([]byte(strings.Replace(base, "mailto:owner@example.com", contact, 1)+"    language: ko\n"), "test"); err != nil {
			t.Fatal(err)
		}
	}
	// An explicit empty host list disables all endpoints and survives config show.
	cfg, err := Parse([]byte(base+"    allowed_hosts: []\n"), "test")
	if err != nil || len(cfg.Approval.Push.AllowedHosts) != 0 {
		t.Fatal("empty allowlist changed", err)
	}
}
