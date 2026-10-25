// Package authn verifies bearer tokens for static and external auth modes.
package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

// Static verifies pre-shared tokens configured by their SHA-256.
func Static(tokens []config.StaticToken) (auth.TokenVerifier, error) {
	type entry struct {
		hash   []byte
		name   string
		scopes []string
		tools  []string
	}
	entries := make([]entry, 0, len(tokens))
	names := make(map[string]bool)
	for _, t := range tokens {
		if t.Name == "" || names[t.Name] {
			return nil, fmt.Errorf("static token names must be nonempty and unique")
		}
		names[t.Name] = true
		hash, err := hex.DecodeString(t.SHA256)
		if err != nil || len(t.SHA256) != 64 {
			return nil, fmt.Errorf("static token SHA256 must be 64 hex characters")
		}
		entries = append(entries, entry{hash, t.Name, slices.Clone(t.Scopes), slices.Clone(t.Tools)})
	}
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token == "" || len(token) > maxTokenBytes {
			return nil, auth.ErrInvalidToken
		}
		sum := sha256.Sum256([]byte(token))
		match := -1
		for i, e := range entries {
			if subtle.ConstantTimeCompare(sum[:], e.hash) == 1 {
				match = i
			}
		}
		if match < 0 {
			return nil, auth.ErrInvalidToken
		}
		e := entries[match]
		return &auth.TokenInfo{Scopes: slices.Clone(e.scopes), UserID: "static:" + e.name, Expiration: time.Now().Add(time.Hour),
			Extra: map[string]any{"tools": slices.Clone(e.tools)}}, nil
	}, nil
}

const maxTokenBytes = 64 << 10
