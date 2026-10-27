package authn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

func TestStatic(t *testing.T) {
	hash := sha256.Sum256([]byte("secret"))
	token := config.StaticToken{Name: "reader", SHA256: hex.EncodeToString(hash[:]), Scopes: []string{config.ScopeRead}, Tools: []string{"host_*"}}
	for _, tt := range []struct {
		name    string
		tokens  []config.StaticToken
		invalid bool
	}{
		{"valid", []config.StaticToken{token}, false},
		{"uppercase", []config.StaticToken{{Name: "reader", SHA256: strings.ToUpper(token.SHA256)}}, false},
		{"empty", nil, false},
		{"duplicate", []config.StaticToken{token, token}, true},
		{"short", []config.StaticToken{{Name: "a", SHA256: "ab"}}, true},
		{"nonhex", []config.StaticToken{{Name: "a", SHA256: strings.Repeat("z", 64)}}, true},
		{"empty name", []config.StaticToken{{SHA256: token.SHA256}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Static(tt.tokens)
			if (err != nil) != tt.invalid {
				t.Fatalf("error = %v", err)
			}
		})
	}
	v, err := Static([]config.StaticToken{token})
	if err != nil {
		t.Fatal(err)
	}
	token.Scopes[0] = config.ScopeWrite
	token.Tools[0] = "note_*"
	for _, raw := range []string{"secret", "invalid", "", strings.Repeat("x", maxTokenBytes+1)} {
		info, err := v(context.Background(), raw, nil)
		if raw != "secret" {
			if !errors.Is(err, auth.ErrInvalidToken) || info != nil {
				t.Fatalf("accepted invalid token")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.UserID != "static:reader" || !reflect.DeepEqual(info.Scopes, []string{config.ScopeRead}) || time.Until(info.Expiration) < 59*time.Minute {
			t.Fatalf("unexpected info: %+v", info)
		}
		tools, ok := info.Extra["tools"].([]string)
		if !ok || !reflect.DeepEqual(tools, []string{"host_*"}) {
			t.Fatalf("unexpected tools: %v", info.Extra)
		}
		info.Scopes[0] = config.ScopeWrite
		tools[0] = "*"
		again, _ := v(context.Background(), raw, nil)
		if again.Scopes[0] != config.ScopeRead {
			t.Fatal("mutable scopes")
		}
		if !reflect.DeepEqual(again.Extra["tools"], []string{"host_*"}) {
			t.Fatal("mutable tools")
		}
	}
}
