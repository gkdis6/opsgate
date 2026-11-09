package authn

import (
	"encoding/json"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
)

// Scope claims of any shape yield only known scopes, each at most once.
func FuzzTokenScopes(f *testing.F) {
	for _, seed := range []string{`{"scope":"ops:read ops:write"}`, `{"scp":["ops:read","admin"]}`, `{"scope":["ops:read"]}`, `{"scope":null}`, `{"scp":"ops:read ops:read"}`, `{}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var claims map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &claims) != nil {
			return
		}
		scopes, ok := tokenScopes(claims)
		if !ok {
			return
		}
		seen := map[string]bool{}
		for _, s := range scopes {
			if !config.KnownScope(s) || seen[s] {
				t.Fatalf("scopes %v from %s", scopes, raw)
			}
			seen[s] = true
		}
	})
}
