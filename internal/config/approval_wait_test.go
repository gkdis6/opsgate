package config

import (
	"strings"
	"testing"
	"time"
)

func TestApprovalWait(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       time.Duration
	}{
		{"plain", "{}", 0},
		{"mode", "mode: kubernetes", 25 * time.Second},
		{"explicit web", "approval:\n  web: true", 25 * time.Second},
		{"mode web disabled", "mode: kubernetes\napproval:\n  web: false", 0},
		{"mode external", "mode: kubernetes\nauth:\n  mode: external", 0},
		{"explicit zero", "mode: kubernetes\napproval:\n  wait: 0s", 0},
		{"maximum", "approval:\n  web: true\n  wait: 55s", 55 * time.Second},
		{"without web", "approval:\n  wait: 2s", 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.yaml), "test")
			if err != nil || cfg.Approval.Wait != tc.want {
				t.Fatalf("wait = %v, err %v", cfg.Approval.Wait, err)
			}
			effective, err := cfg.Effective()
			if err != nil {
				t.Fatal(err)
			}
			again, err := Parse(effective, "effective")
			if err != nil || again.Approval.Wait != tc.want {
				t.Fatalf("round trip wait = %v, err %v", again.Approval.Wait, err)
			}
		})
	}
	for _, value := range []string{"-1ns", "55.000000001s", "1m", "nope"} {
		if _, err := Parse([]byte("approval:\n  wait: "+value), "test"); err == nil || value != "nope" && !strings.Contains(err.Error(), "approval.wait") {
			t.Fatalf("invalid wait %q: %v", value, err)
		}
	}
	if Default().Approval.Wait != 0 {
		t.Fatal("plain default waits")
	}
}
