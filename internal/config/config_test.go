package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsMultipleDocuments(t *testing.T) {
	for _, extra := range []string{"server:\n  name: ignored\n", "", "[invalid"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("server:\n  name: first\n---\n"+extra), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "multiple YAML documents are not supported") {
			t.Fatalf("extra %q: Load() = %v", extra, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  name: single\n...\n# trailing comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(path); err != nil || cfg.Server.Name != "single" {
		t.Fatalf("single document: %v, %v", cfg, err)
	}
}

func TestValidateCommandPrefix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []string
		valid  bool
	}{
		{name: "empty", valid: true},
		{name: "absolute", prefix: []string{"/usr/bin/sudo"}, valid: true},
		{name: "flags after executable", prefix: []string{"/usr/bin/sudo", "-n"}, valid: true},
		{name: "paths after executable", prefix: []string{"/usr/bin/env", "/usr/bin/sudo", "-n"}, valid: true},
		{name: "flag first", prefix: []string{"-n"}},
		{name: "flag before executable", prefix: []string{"-n", "/usr/bin/sudo"}},
		{name: "relative executable", prefix: []string{"sudo", "-n"}},
		{name: "empty executable", prefix: []string{"", "-n"}},
		{name: "relative argument", prefix: []string{"/usr/bin/sudo", "sudo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Providers.Host.CommandPrefix = tc.prefix
			if err := cfg.Validate(); (err == nil) != tc.valid {
				t.Errorf("Validate() = %v, valid = %v", err, tc.valid)
			}
		})
	}
}
