package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigAndCredentials(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]string{}
	for name, value := range map[string]string{"hmac": "0123456789abcdef", "mcp": "read-token", "model": "private-key", "discord": "https://discord.com/api/webhooks/123/test-token"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	c := Default()
	c.SecretFile = paths["hmac"]
	c.MCP.Endpoint = "http://127.0.0.1:8788/mcp"
	c.MCP.TokenFile = paths["mcp"]
	c.Model.Name = "chosen-model"
	c.Model.APIKeyFile = paths["model"]
	c.DiscordURLFile = paths["discord"]
	if _, err := New(c); err != nil {
		t.Fatal(err)
	}
	cli := c
	cli.Model.Backend = "claude_cli"
	cli.Model.APIKeyFile = ""
	fixture := fixtureModel(t, "pipeline")
	cli.Model.Claude.Binary = fixture.binary
	cli.Model.Claude.ConfigDir = fixture.configDir
	if _, err := New(cli); err != nil {
		t.Fatalf("CLI backend unexpectedly needs API key: %v", err)
	}
	for _, url := range []string{"http://public.example/mcp", "https://user:secret@example.com", "https://example.com/#secret", "https://example.com/?secret=1"} {
		bad := c
		bad.MCP.Endpoint = url
		if err := bad.Validate(); err == nil || strings.Contains(err.Error(), "secret=1") {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	bad := c
	bad.Model.Name = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("implicit model accepted")
	}
	bad = c
	bad.MaxToolCalls = 100
	if err := bad.Validate(); err == nil {
		t.Fatal("unbounded calls")
	}
	if err := os.WriteFile(paths["model"], []byte("private-key\r\nInjected: true"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.yaml")
	for _, yaml := range []string{"unknown: true\n", "listen: localhost:8080\n---\nlisten: localhost:8081\n"} {
		if err := os.WriteFile(p, []byte(yaml), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Fatal("invalid YAML accepted")
		}
	}
}

func TestBackendExampleConfigurations(t *testing.T) {
	for file, backend := range map[string]string{"analyzer.example.yaml": "openai_compatible", "analyzer.claude.example.yaml": "claude_cli", "analyzer.codex.example.yaml": "codex_cli"} {
		c, err := Load(filepath.Join("..", "..", file))
		if err != nil || c.Model.Backend != backend {
			t.Fatalf("%s backend=%s err=%v", file, c.Model.Backend, err)
		}
	}
}
