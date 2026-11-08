package config

import (
	"strings"
	"testing"
)

func TestRemovedProviderConfig(t *testing.T) {
	for _, section := range []string{"{}", "null", "{enabled: false}", "{enabled: true, runner_socket: /tmp/old.sock}"} {
		_, err := Parse([]byte("providers:\n  dev: "+section+"\n"), "old.yaml")
		if err == nil || !strings.Contains(err.Error(), "providers.dev was removed in v0.4.0; delete this section") {
			t.Fatalf("section %s: %v", section, err)
		}
	}
}

func TestRemovedScopeConfig(t *testing.T) {
	for _, data := range []string{
		"stdio: {scopes: [ops:read, 'dev:run']}",
		"auth: {builtin: {users: [{subject: 'github:alice', scopes: [ops:read, 'dev:run']}]}}",
		"auth: {external: {users: [{subject: 'claim:alice', scopes: [ops:read, 'dev:run']}]}}",
		"auth: {static: [{name: alice, scopes: [ops:read, 'dev:run']}]}",
	} {
		_, err := Parse([]byte(data), "old.yaml")
		if err == nil || !strings.Contains(err.Error(), "scope dev:run was removed in v0.4.0") {
			t.Fatalf("config %s: %v", data, err)
		}
	}
}
