package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/oauth"
)

func TestBuiltinVerifierCarriesToolPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, configured, subject string
		tools                     []string
	}{
		{"unrestricted", "github:alice", "github:alice", nil},
		{"github case folding", "github:alice", "github:ALICE", []string{"k8s_*"}},
		{"email case folding", "email:alice@example.com", "email:Alice@Example.com", []string{"note_read", "note_list"}},
		{"OIDC subject", "oidc:issuer:Alice", "oidc:issuer:Alice", []string{"k8s_overview"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const issuer = "https://ops.example"
			const raw = "og_at_policy-test-token"
			hash := sha256.Sum256([]byte(raw))
			state := map[string]any{
				"version": 1, "issuer": issuer, "resource": issuer + "/mcp",
				// Verify only honours tokens of registered, approved clients.
				"clients": map[string]any{"test-client": map[string]any{
					"client_id": "test-client", "redirect_uris": []string{"https://client.example/cb"}, "client_name": "test",
					"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"},
					"created": time.Now(), "last_used": time.Now(),
				}}, "refresh": map[string]any{}, "revoked_families": map[string]any{}, "codes": map[string]any{},
				"access": map[string]any{hex.EncodeToString(hash[:]): map[string]any{
					"ClientID": "test-client", "Subject": tc.subject, "Scopes": config.Scopes,
					"Family": "test-family", "Expires": time.Now().Add(time.Hour),
				}},
			}
			cfg := config.Default().Auth.Builtin
			cfg.StateFile = filepath.Join(t.TempDir(), "oauth-state")
			cfg.Users = []config.User{{Subject: tc.configured, Scopes: []string{config.ScopeRead}, Tools: tc.tools}}
			cfg.Upstream = config.Upstream{Provider: "github", ClientID: "test-client", ClientSecretEnv: "OPSGATE_POLICY_TEST_SECRET"}
			t.Setenv("OPSGATE_POLICY_TEST_SECRET", "test-only-secret")
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.StateFile, data, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := oauth.New(cfg, issuer, issuer+"/mcp")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			info, err := s.VerifyWithTools(context.Background(), raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			tools, ok := info.Extra["tools"].([]string)
			if !ok || !slices.Equal(tools, tc.tools) || info.Extra["client_id"] != "test-client" || !slices.Equal(info.Scopes, []string{config.ScopeRead}) {
				t.Fatalf("token info = %+v", info)
			}
			if len(tools) > 0 {
				tools[0] = "*"
				again, err := s.VerifyWithTools(context.Background(), raw, nil)
				if err != nil || !slices.Equal(again.Extra["tools"].([]string), tc.tools) {
					t.Fatalf("mutable tool policy: %+v, error = %v", again, err)
				}
			}
			if _, err := s.VerifyWithTools(context.Background(), "invalid", nil); err == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
}
