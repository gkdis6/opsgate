package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

func TestExternal(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name           string
		mutate         func(map[string]any)
		users          []config.User
		alg            jose.SignatureAlgorithm
		kid            string
		fallback       bool
		customAudience bool
		wantScopes     []string
		wantTools      []string
		invalid        bool
	}{
		{name: "valid", wantScopes: []string{config.ScopeRead, config.ScopeWrite}},
		{name: "wrong audience", mutate: func(c map[string]any) { c["aud"] = "other" }, invalid: true},
		{name: "wrong issuer", mutate: func(c map[string]any) { c["iss"] = "https://other" }, invalid: true},
		{name: "expired", mutate: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, invalid: true},
		{name: "missing expiry", mutate: func(c map[string]any) { delete(c, "exp") }, invalid: true},
		{name: "none", alg: "none", invalid: true},
		{name: "key confusion", alg: jose.HS256, invalid: true},
		{name: "unknown kid", kid: "unknown", invalid: true},
		{name: "user forbidden", users: []config.User{{Subject: "claim:other", Scopes: []string{config.ScopeRead}}}, invalid: true},
		{name: "intersection", users: []config.User{{Subject: "claim:alice", Scopes: []string{config.ScopeRead}}}, wantScopes: []string{config.ScopeRead}},
		{name: "tool restrictions", users: []config.User{{Subject: "claim:alice", Scopes: []string{config.ScopeRead}, Tools: []string{"host_*"}}}, wantScopes: []string{config.ScopeRead}, wantTools: []string{"host_*"}},
		{name: "no granted scopes", users: []config.User{{Subject: "claim:alice"}}},
		{name: "fallback", fallback: true, wantScopes: []string{config.ScopeRead, config.ScopeWrite}},
		{name: "audience override", customAudience: true, wantScopes: []string{config.ScopeRead, config.ScopeWrite}},
		{name: "scp string", mutate: func(c map[string]any) { delete(c, "scope"); c["scp"] = "ops:read ignored" }, wantScopes: []string{config.ScopeRead}},
		{name: "scp array", mutate: func(c map[string]any) { delete(c, "scope"); c["scp"] = []string{"ops:read", "ignored", "ops:read"} }, wantScopes: []string{config.ScopeRead}},
		{name: "bad scope type", mutate: func(c map[string]any) { c["scope"] = []string{"ops:read"} }, invalid: true},
		{name: "null scope", mutate: func(c map[string]any) { c["scope"] = nil }, invalid: true},
		{name: "missing user", mutate: func(c map[string]any) { delete(c, "uid") }, invalid: true},
		{name: "bad user type", mutate: func(c map[string]any) { c["uid"] = 123 }, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var issuer string
			ts := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					if tt.fallback {
						http.NotFound(w, r)
						return
					}
					fallthrough
				case "/.well-known/oauth-authorization-server":
					json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": issuer + "/keys"})
				case "/keys":
					json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "known", Algorithm: "RS256", Use: "sig"}}})
				default:
					http.NotFound(w, r)
				}
			}))
			issuer = ts.URL
			cfg := config.ExternalAuth{Issuer: issuer, UserClaim: "uid", Users: tt.users}
			audience := "resource"
			if tt.customAudience {
				cfg.Audience = "override"
				audience = "override"
			}
			v, err := External(oidc.ClientContext(context.Background(), ts.Client()), cfg, "resource")
			if err != nil {
				t.Fatal(err)
			}
			claims := map[string]any{"iss": issuer, "aud": audience, "sub": "subject", "uid": "alice", "exp": time.Now().Add(time.Hour).Unix(), "scope": "ops:read ignored ops:write"}
			if tt.mutate != nil {
				tt.mutate(claims)
			}
			payload, _ := json.Marshal(claims)
			alg := tt.alg
			if alg == "" {
				alg = jose.RS256
			}
			kid := tt.kid
			if kid == "" {
				kid = "known"
			}
			var raw string
			if alg == "none" {
				raw = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
			} else {
				var signingKey any = key
				if alg == jose.HS256 {
					signingKey = x509.MarshalPKCS1PublicKey(&key.PublicKey)
				}
				signer, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: signingKey}, (&jose.SignerOptions{}).WithHeader("kid", kid))
				if err != nil {
					t.Fatal(err)
				}
				object, err := signer.Sign(payload)
				if err != nil {
					t.Fatal(err)
				}
				raw, err = object.CompactSerialize()
				if err != nil {
					t.Fatal(err)
				}
			}
			info, err := v(context.Background(), raw, nil)
			if tt.invalid {
				if !errors.Is(err, auth.ErrInvalidToken) || info != nil {
					t.Fatalf("invalid token accepted: %+v %v", info, err)
				}
				if strings.Contains(err.Error(), raw) {
					t.Fatal("token leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.UserID != "alice" || !slices.Equal(info.Scopes, tt.wantScopes) || time.Until(info.Expiration) < 59*time.Minute {
				t.Fatalf("unexpected info: %+v", info)
			}
			if len(tt.users) > 0 {
				tools, ok := info.Extra["tools"].([]string)
				if !ok || !slices.Equal(tools, tt.wantTools) {
					t.Fatalf("unexpected tools: %v", info.Extra)
				}
				if len(tools) > 0 {
					tools[0] = "*"
					tt.users[0].Tools[0] = "*"
					again, err := v(context.Background(), raw, nil)
					if err != nil || !slices.Equal(again.Extra["tools"].([]string), tt.wantTools) {
						t.Fatalf("mutable tools: %v %v", again, err)
					}
				}
			} else if _, exists := info.Extra["tools"]; exists {
				t.Fatalf("tool restrictions without configured users: %v", info.Extra)
			}
		})
	}
}

func TestDiscoveryRejects(t *testing.T) {
	for _, tt := range []struct {
		name   string
		body   string
		status int
	}{
		{"mismatch", `{"issuer":"https://other","jwks_uri":"https://other/keys"}`, 200},
		{"invalid json", `{`, 200},
		{"missing jwks", `{"issuer":"ISSUER"}`, 200},
		{"oversized", strings.Repeat(" ", 1<<20) + `{}`, 200},
		{"unavailable", "", 404},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var issuer string
			ts := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(strings.ReplaceAll(tt.body, "ISSUER", issuer)))
			}))
			issuer = ts.URL
			if _, err := External(oidc.ClientContext(context.Background(), ts.Client()), config.ExternalAuth{Issuer: issuer}, "resource"); err == nil {
				t.Fatal("accepted invalid metadata")
			}
		})
	}
}

type issuerServer struct {
	URL    string
	client *http.Client
}
type issuerTransport struct{ handler http.Handler }

func (tr issuerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	tr.handler.ServeHTTP(recorder, req)
	return recorder.Result(), nil
}
func newIssuerServer(handler http.Handler) *issuerServer {
	return &issuerServer{URL: "https://issuer.example", client: &http.Client{Transport: issuerTransport{handler}}}
}
func (s *issuerServer) Client() *http.Client { return s.client }

func TestRFC8414IssuerPath(t *testing.T) {
	const issuer = "https://issuer.example/tenant"
	var paths []string
	server := newIssuerServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/.well-known/oauth-authorization-server/tenant" {
			json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": "https://issuer.example/keys"})
		} else {
			http.NotFound(w, r)
		}
	}))
	_, err := External(oidc.ClientContext(context.Background(), server.Client()), config.ExternalAuth{Issuer: issuer}, "resource")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paths, []string{"/tenant/.well-known/openid-configuration", "/.well-known/oauth-authorization-server/tenant"}) {
		t.Fatalf("discovery paths=%v", paths)
	}
}

func TestTokenScopes(t *testing.T) {
	for _, tt := range []struct {
		name, claims string
		want         []string
		valid        bool
	}{
		{"scope", `{"scope":"ops:read ignored ops:read ops:write"}`, []string{config.ScopeRead, config.ScopeWrite}, true},
		{"scp", `{"scp":["ignored","ops:write"]}`, []string{config.ScopeWrite}, true},
		{"scope precedence", `{"scope":"ops:read","scp":"ops:write"}`, []string{config.ScopeRead}, true},
		{"missing", `{}`, nil, true},
		{"null", `{"scope":null}`, nil, false},
		{"number", `{"scp":7}`, nil, false},
		{"array null", `{"scp":["ops:read",null]}`, nil, false},
		{"array object", `{"scp":[{}]}`, nil, false},
		{"scope array", `{"scope":["ops:read"]}`, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var claims map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.claims), &claims); err != nil {
				t.Fatal(err)
			}
			got, valid := tokenScopes(claims)
			if valid != tt.valid || !slices.Equal(got, tt.want) {
				t.Fatalf("scopes=%v valid=%v", got, valid)
			}
		})
	}
}

func TestExternalEndpointTransportSecurity(t *testing.T) {
	for _, tt := range []struct {
		name, issuer, jwks string
		allowed            bool
	}{
		{"HTTPS", "https://issuer.example", "https://issuer.example/keys", true},
		{"HTTP issuer", "http://issuer.example", "https://issuer.example/keys", false},
		{"HTTP JWKS", "https://issuer.example", "http://issuer.example/keys", false},
		{"localhost", "http://localhost:8080", "http://localhost:8080/keys", true},
		{"IPv4 loopback", "http://127.0.0.1:8080", "http://127.0.0.1:8080/keys", true},
		{"IPv4 loopback range", "http://127.0.0.2:8080", "http://127.0.0.2:8080/keys", true},
		{"IPv6 loopback", "http://[::1]:8080", "http://[::1]:8080/keys", true},
		{"localhost suffix", "http://localhost.evil", "http://localhost.evil/keys", false},
		{"private network", "http://192.168.1.1", "http://192.168.1.1/keys", false},
		{"JWKS localhost suffix", "https://issuer.example", "http://localhost.evil/keys", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var fetched []string
			client := &http.Client{Transport: issuerTransport{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fetched = append(fetched, r.URL.String())
				json.NewEncoder(w).Encode(map[string]string{"issuer": tt.issuer, "jwks_uri": tt.jwks})
			})}}
			_, err := External(oidc.ClientContext(context.Background(), client), config.ExternalAuth{Issuer: tt.issuer}, "resource")
			if (err == nil) != tt.allowed {
				t.Fatalf("allowed=%v error=%v", tt.allowed, err)
			}
			if !validEndpoint(tt.issuer) && len(fetched) != 0 {
				t.Fatal("fetched insecure discovery endpoint")
			}
		})
	}
}

func TestDiscoveryRejectsInsecureIssuerBeforeFetching(t *testing.T) {
	client := &http.Client{Transport: issuerTransport{http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("insecure discovery request sent") })}}
	if _, err := discover(context.Background(), client, "http://issuer.example"); err == nil {
		t.Fatal("insecure discovery accepted")
	}
}
