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

func TestValidateHTTPRejectsCredentialsInPublicURL(t *testing.T) {
	c := Default()
	c.HTTP.PublicURL = "https://user:pass@mcp.example.com"
	c.Auth.Mode = AuthStatic
	if err := c.ValidateHTTP(); err == nil {
		t.Fatal("public_url with userinfo accepted")
	}
}
