package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

func TestExternalJWKSRefreshCooldown(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	server := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/keys" {
			fetches.Add(1)
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: public, KeyID: "known"}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"issuer": "https://issuer.example", "jwks_uri": "https://issuer.example/keys"})
	}))
	verify, err := External(oidc.ClientContext(context.Background(), server.Client()), config.ExternalAuth{Issuer: server.URL}, "resource")
	if err != nil {
		t.Fatal(err)
	}
	valid := jwksTestToken(t, private, "known")
	unknown := jwksTestToken(t, private, "unknown")
	_, forgedKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := jwksTestToken(t, forgedKey, "known")
	for range 10 {
		for _, raw := range []string{unknown, forged} {
			if _, err := verify(context.Background(), raw, nil); !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("forgery error=%v", err)
			}
		}
		if _, err := verify(context.Background(), valid, nil); err != nil {
			t.Fatalf("cached valid key failed: %v", err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("JWKS fetches=%d, want 1", got)
	}
}

func TestJWKSRefreshRotationAndFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotation", true: "fetch failure"}[failure], func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			var fetches atomic.Int32
			var rotated, unavailable atomic.Bool
			server := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fetches.Add(1)
				if unavailable.Load() {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				keys := []jose.JSONWebKey{{Key: public, KeyID: "old"}}
				if rotated.Load() {
					keys = append(keys, jose.JSONWebKey{Key: public, KeyID: "new"})
				}
				json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: keys})
			}))
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			client := *server.Client()
			client.Transport = &jwksTransport{base: client.Transport, now: func() time.Time { return now }}
			newVerifier := func() *oidc.IDTokenVerifier {
				keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), &client), server.URL+"/keys")
				return oidc.NewVerifier(server.URL, keys, &oidc.Config{ClientID: "resource", SupportedSigningAlgs: []string{oidc.EdDSA}})
			}
			cached := newVerifier()
			oldToken, newToken := jwksTestToken(t, private, "old"), jwksTestToken(t, private, "new")
			verify := func(raw string, valid bool) {
				t.Helper()
				verifier := cached
				if raw == newToken {
					// Isolate the shared cooldown from go-oidc's background cache updates.
					verifier = newVerifier()
				}
				_, err := verifier.Verify(context.Background(), raw)
				if (err == nil) != valid {
					t.Fatalf("valid=%v error=%v", valid, err)
				}
			}
			verify(oldToken, true)
			rotated.Store(true)
			verify(newToken, false)
			now = now.Add(jwksRefreshInterval - time.Nanosecond)
			verify(newToken, false)
			if got := fetches.Load(); got != 1 {
				t.Fatalf("fetches during cooldown=%d", got)
			}
			now = now.Add(time.Nanosecond)
			unavailable.Store(failure)
			verify(newToken, !failure)
			verify(oldToken, true)
			if failure {
				for range 5 {
					verify(newToken, false)
				}
				unavailable.Store(false)
				now = now.Add(jwksRefreshInterval)
				verify(newToken, true)
			}
			want := int32(2)
			if failure {
				want++
			}
			if got := fetches.Load(); got != want {
				t.Fatalf("fetches=%d want=%d", got, want)
			}
		})
	}
}

func TestJWKSConcurrentRefreshes(t *testing.T) {
	var fetches atomic.Int32
	server := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Write([]byte(`{"keys":[]}`))
	}))
	transport := &jwksTransport{base: server.Client().Transport, now: time.Now}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			r, err := transport.RoundTrip(mustJWKSRequest(t, server.URL))
			if err == nil {
				r.Body.Close()
			}
		})
	}
	wg.Wait()
	if got := fetches.Load(); got != 1 {
		t.Fatalf("concurrent fetches=%d", got)
	}
}

func mustJWKSRequest(t *testing.T, endpoint string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func jwksTestToken(t *testing.T, private ed25519.PrivateKey, kid string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: private}, (&jose.SignerOptions{}).WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"iss": "https://issuer.example", "aud": "resource", "sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
