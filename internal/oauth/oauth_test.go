package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/gkdis6/opsgate/internal/config"
)

type fixture struct {
	closeErrorExpected             bool
	s                              *Server
	web                            *testSite
	browser                        *http.Client
	id, secret, verifier, redirect string
}

type fixtureIdentity struct {
	issuer, subject, hostedDomain string
	omitEmail                     bool
}

func newFixture(t *testing.T, oidcMode, verified, goodNonce bool, login string) *fixture {
	return newFixtureIdentity(t, oidcMode, verified, goodNonce, login, fixtureIdentity{})
}

func newFixtureIdentity(t *testing.T, oidcMode, verified, goodNonce bool, login string, identity fixtureIdentity) *fixture {
	t.Helper()
	t.Setenv("OG_TEST_SECRET", "upstream-secret")
	origin := identity.issuer
	if origin == "" {
		origin = "https://upstream.test"
	}
	subject := identity.subject
	if subject == "" {
		subject = "123"
	}
	var nonce, upstreamChallenge string
	router := &testTransport{handlers: map[string]http.Handler{}}
	var signer jose.Signer
	var jwk jose.JSONWebKey
	if oidcMode {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		signer, err = jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
		if err != nil {
			t.Fatal(err)
		}
		jwk = jose.JSONWebKey{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}
	}
	var downstream string
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": origin, "authorization_endpoint": origin + "/auth", "token_endpoint": origin + "/token", "jwks_uri": origin + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
		case "/auth":
			values := r.URL.Query()
			if values.Get("response_type") != "code" || values.Get("client_id") != "upstream-client" || values.Get("redirect_uri") != downstream+"/oauth/callback" || values.Get("code_challenge_method") != "S256" || values.Get("state") == "" {
				t.Error("invalid upstream authorization request")
				w.WriteHeader(400)
				return
			}
			upstreamChallenge = values.Get("code_challenge")
			nonce = r.URL.Query().Get("nonce")
			http.Redirect(w, r, downstream+"/oauth/callback?code=upstream-code&state="+url.QueryEscape(r.URL.Query().Get("state")), 302)
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			id, secret, basic := r.BasicAuth()
			if !basic {
				id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
			}
			if id != "upstream-client" || secret != "upstream-secret" || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "upstream-code" || r.PostForm.Get("redirect_uri") != downstream+"/oauth/callback" || oauth2.S256ChallengeFromVerifier(r.PostForm.Get("code_verifier")) != upstreamChallenge {
				t.Error("invalid upstream token exchange")
				w.WriteHeader(400)
				return
			}
			response := map[string]any{"access_token": "upstream-token", "token_type": "Bearer"}
			if oidcMode {
				n := nonce
				if !goodNonce {
					n = "wrong-nonce"
				}
				values := map[string]any{"iss": origin, "sub": subject, "aud": "upstream-client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "nonce": n, "email_verified": verified, "hd": identity.hostedDomain}
				if !identity.omitEmail {
					values["email"] = login
				}
				claims, _ := json.Marshal(values)
				signed, err := signer.Sign(claims)
				if err != nil {
					t.Error(err)
					return
				}
				raw, _ := signed.CompactSerialize()
				response["id_token"] = raw
			}
			json.NewEncoder(w).Encode(response)
		case "/user":
			json.NewEncoder(w).Encode(map[string]any{"login": login})
		default:
			http.NotFound(w, r)
		}
	})
	router.handlers[origin] = upstreamHandler
	web := &testSite{URL: "https://opsgate.test", transport: router}
	downstream = web.URL
	cfg := config.Default().Auth.Builtin
	cfg.Users = []config.User{{Subject: "github:alice", Scopes: []string{"ops:read", "ops:write"}}, {Subject: "email:alice@example.com", Scopes: []string{"ops:read", "ops:write"}}}
	cfg.Upstream = config.Upstream{Provider: "github", ClientID: "upstream-client", ClientSecretEnv: "OG_TEST_SECRET"}
	if oidcMode {
		cfg.Upstream.Provider = "oidc"
		cfg.Upstream.Issuer = origin
	}
	s, err := newServer(cfg, web.URL, web.URL+"/mcp", router)
	if err != nil {
		t.Fatal(err)
	}
	if !oidcMode {
		s.upstream.Endpoint = oauth2.Endpoint{AuthURL: origin + "/auth", TokenURL: origin + "/token", AuthStyle: oauth2.AuthStyleInParams}
		s.githubUserURL = origin + "/user"
	}
	s.registrationLimit = rate.NewLimiter(rate.Inf, 100)
	mux := http.NewServeMux()
	s.Routes(mux)
	mux.Handle("/private", s.RequireUser(func(w http.ResponseWriter, r *http.Request, subject string) { fmt.Fprint(w, subject) }))
	router.handlers[web.URL] = mux
	jar, _ := cookiejar.New(nil)
	browser := web.Client()
	browser.Jar = jar
	f := &fixture{s: s, web: web, browser: browser, redirect: "https://client.example/callback", verifier: oauth2.GenerateVerifier()}
	t.Cleanup(func() {
		if err := s.Close(); err != nil && !f.closeErrorExpected {
			t.Error(err)
		}
	})
	f.id, f.secret = f.register(t, "none", "<script>alert(1)</script>")
	return f
}
func (f *fixture) request(t *testing.T, method, path, body string, follow bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.web.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c := *f.browser
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
func (f *fixture) register(t *testing.T, method, name string) (string, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"redirect_uris": []string{f.redirect}, "client_name": name, "token_endpoint_auth_method": method})
	req, _ := http.NewRequest("POST", f.web.URL+"/oauth/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]any
	json.NewDecoder(resp.Body).Decode(&got)
	if resp.StatusCode != 201 {
		t.Fatalf("registration: %d %v", resp.StatusCode, got)
	}
	id, _ := got["client_id"].(string)
	secret, _ := got["client_secret"].(string)
	return id, secret
}
func (f *fixture) authQuery() url.Values {
	return url.Values{"client_id": {f.id}, "redirect_uri": {f.redirect}, "response_type": {"code"}, "code_challenge": {oauth2.S256ChallengeFromVerifier(f.verifier)}, "code_challenge_method": {"S256"}, "scope": {"ops:read ops:write"}, "state": {"client-state"}}
}
func (f *fixture) consent(t *testing.T) string {
	t.Helper()
	resp := f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", true)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("consent: %d %s", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "<script>") || !strings.Contains(string(b), "&lt;script&gt;") {
		t.Fatalf("client_name not escaped: %s", b)
	}
	if !strings.Contains(string(b), "client.example") || !strings.Contains(strings.ToLower(string(b)), "unverified") {
		t.Fatalf("missing consent identity: %s", b)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action") {
		t.Fatal("unsafe HTML headers")
	}
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(b)
	if len(match) != 2 {
		t.Fatalf("no csrf token: %s", b)
	}
	return string(match[1])
}
func (f *fixture) code(t *testing.T) string {
	t.Helper()
	csrf := f.consent(t)
	r := f.request(t, "POST", "/oauth/consent", url.Values{"csrf_token": {csrf}, "decision": {"approve"}}.Encode(), false)
	u, _ := url.Parse(r.Header.Get("Location"))
	if u.Query().Get("iss") != f.web.URL || u.Query().Get("state") != "client-state" {
		t.Fatal("missing response binding")
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatal("missing code")
	}
	return code
}
func (f *fixture) tokenForm(code string) url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {f.id}, "code": {code}, "redirect_uri": {f.redirect}, "code_verifier": {f.verifier}}
}
func (f *fixture) token(t *testing.T, v url.Values) (int, map[string]any) {
	t.Helper()
	r := f.request(t, "POST", "/oauth/token", v.Encode(), false)
	var got map[string]any
	json.NewDecoder(r.Body).Decode(&got)
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("token caching enabled")
	}
	return r.StatusCode, got
}
func (f *fixture) verify(t *testing.T, token string, valid bool) {
	t.Helper()
	info, err := f.s.Verify(context.Background(), token, nil)
	if (err == nil) != valid {
		t.Fatalf("Verify valid=%v got info=%v err=%v", valid, info, err)
	}
}

func TestAuthorizeAttacks(t *testing.T) {
	for _, tt := range []struct{ name, key, value, oauthError string }{
		{"unknown client", "client_id", "unknown", "Unknown client"},
		{"redirect mismatch", "redirect_uri", "https://evil.example/cb", "Unknown client"},
		{"plain PKCE", "code_challenge_method", "plain", "invalid_request"},
		{"missing PKCE method", "code_challenge_method", "", "invalid_request"},
		{"missing challenge", "code_challenge", "", "invalid_request"},
		{"resource", "resource", "https://evil.example/mcp", "invalid_target"},
		{"scope", "scope", "admin", "invalid_scope"},
		{"response type", "response_type", "token", "unsupported_response_type"},
		{"missing response type", "response_type", "", "invalid_request"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			v := f.authQuery()
			v.Set(tt.key, tt.value)
			r := f.request(t, "GET", "/oauth/authorize?"+v.Encode(), "", false)
			body, _ := io.ReadAll(r.Body)
			if r.StatusCode != 400 || r.Header.Get("Location") != "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(body), tt.oauthError) {
				t.Fatalf("authorization error must stay local: %d %s %s", r.StatusCode, r.Header.Get("Location"), body)
			}
		})
	}
}
func TestCodeAttacks(t *testing.T) {
	for _, attack := range []string{"wrong verifier", "redirect mismatch", "other client", "resource mismatch", "expired", "reuse"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			code := f.code(t)
			v := f.tokenForm(code)
			switch attack {
			case "wrong verifier":
				v.Set("code_verifier", oauth2.GenerateVerifier())
			case "redirect mismatch":
				v.Set("redirect_uri", "https://evil.example")
			case "other client":
				id, _ := f.register(t, "none", "other")
				v.Set("client_id", id)
			case "resource mismatch":
				v.Set("resource", "https://evil.example")
			case "expired":
				f.s.mu.Lock()
				f.s.codes[hash(code)].Expires = time.Now().Add(-time.Second)
				f.s.mu.Unlock()
			case "reuse":
				status, tokens := f.token(t, v)
				if status != 200 {
					t.Fatal(tokens)
				}
				f.verify(t, tokens["access_token"].(string), true)
				status, _ = f.token(t, v)
				if status == 200 {
					t.Fatal("reused code accepted")
				}
				f.verify(t, tokens["access_token"].(string), false)
				_, got := f.token(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {f.id}, "refresh_token": {tokens["refresh_token"].(string)}})
				if got["error"] != "invalid_grant" {
					t.Fatal(got)
				}
				return
			}
			status, _ := f.token(t, v)
			if status == 200 {
				t.Fatal("attack accepted")
			}
		})
	}
}
func TestRefreshAttacks(t *testing.T) {
	for _, attack := range []string{"reuse", "other client", "escalation"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			_, tokens := f.token(t, f.tokenForm(f.code(t)))
			v := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.id}, "refresh_token": {tokens["refresh_token"].(string)}}
			switch attack {
			case "reuse":
				status, newTokens := f.token(t, v)
				if status != 200 {
					t.Fatal(newTokens)
				}
				status, _ = f.token(t, v)
				if status == 200 {
					t.Fatal("refresh reuse accepted")
				}
				f.verify(t, newTokens["access_token"].(string), false)
				f.verify(t, tokens["access_token"].(string), false)
			case "other client":
				id, _ := f.register(t, "none", "other")
				v.Set("client_id", id)
				status, _ := f.token(t, v)
				if status == 200 {
					t.Fatal("client binding lost")
				}
			case "escalation":
				v.Set("scope", "admin")
				status, got := f.token(t, v)
				if status == 200 || got["error"] != "invalid_scope" {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestConsentCSRF(t *testing.T) {
	for _, attack := range []string{"missing", "wrong", "no cookie", "other cookie"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			csrf := f.consent(t)
			switch attack {
			case "missing":
				csrf = ""
			case "wrong":
				csrf = "wrong"
			case "no cookie":
				jar, _ := cookiejar.New(nil)
				f.browser.Jar = jar
			case "other cookie":
				f.consent(t)
			}
			r := f.request(t, "POST", "/oauth/consent", url.Values{"csrf_token": {csrf}, "decision": {"approve"}}.Encode(), false)
			if r.StatusCode < 400 || r.Header.Get("Location") != "" {
				t.Fatal("CSRF accepted")
			}
		})
	}
}
func TestCallbackAttacks(t *testing.T) {
	for _, attack := range []string{"state", "disallowed", "unverified email", "nonce"} {
		t.Run(attack, func(t *testing.T) {
			login := "alice@example.com"
			if attack == "disallowed" {
				login = "mallory"
			}
			oidcMode := attack == "unverified email" || attack == "nonce"
			if !oidcMode && attack != "disallowed" {
				login = "Alice"
			}
			f := newFixture(t, oidcMode, attack != "unverified email", attack != "nonce", login)
			var r *http.Response
			if attack == "state" {
				f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", false)
				r = f.request(t, "GET", "/oauth/callback?code=anything&state=wrong", "", false)
			} else {
				r = f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", true)
			}
			if r.StatusCode < 400 {
				t.Fatal("callback attack accepted")
			}
		})
	}
}
func TestDCRAttacks(t *testing.T) {
	for _, redirects := range [][]string{{"javascript:alert(1)"}, {"data:text/html,hi"}, {"file:///tmp/a"}, {"vbscript:msgbox(1)"}, {"http://example.com/cb"}, {"https://example.com/#fragment"}, {"https://a", "https://b", "https://c", "https://d", "https://e", "https://f"}, {}} {
		t.Run(fmt.Sprint(redirects), func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			b, _ := json.Marshal(map[string]any{"redirect_uris": redirects})
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/register", strings.NewReader(string(b)))
			req.Header.Set("Content-Type", "application/json")
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode < 400 {
				t.Fatal("invalid registration accepted")
			}
		})
	}
	t.Run("max clients", func(t *testing.T) {
		f := newFixture(t, false, true, true, "Alice")
		f.s.mu.Lock()
		f.s.cfg.MaxClients = 1
		f.s.mu.Unlock()
		req, _ := http.NewRequest("POST", f.web.URL+"/oauth/register", strings.NewReader(`{"redirect_uris":["https://other.example"]}`))
		req.Header.Set("Content-Type", "application/json")
		r, err := f.browser.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode < 400 {
			t.Fatal("limit ignored")
		}
	})
}
func TestAccessAttacks(t *testing.T) {
	for _, attack := range []string{"expired", "revoked", "removed user"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			_, tokens := f.token(t, f.tokenForm(f.code(t)))
			token := tokens["access_token"].(string)
			f.verify(t, token, true)
			switch attack {
			case "expired":
				f.s.mu.Lock()
				f.s.access[hash(token)].Expires = time.Now().Add(-time.Second)
				f.s.mu.Unlock()
			case "revoked":
				r := f.request(t, "POST", "/oauth/revoke", url.Values{"client_id": {f.id}, "token": {token}}.Encode(), false)
				if r.StatusCode != 200 {
					t.Fatal(r.Status)
				}
			case "removed user":
				f.s.mu.Lock()
				f.s.cfg.Users = nil
				f.s.mu.Unlock()
			}
			f.verify(t, token, false)
		})
	}
}
func TestRequireUserRedirects(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	for _, path := range []string{"//evil.example/path", "/\\evil.example/path", "/%5cevil.example", "/%2fevil.example"} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest("GET", f.web.URL+path, nil)
			w := httptest.NewRecorder()
			f.s.RequireUser(func(http.ResponseWriter, *http.Request, string) { t.Fatal("unexpected session") }).ServeHTTP(w, r)
			if w.Code < 400 || w.Header().Get("Location") != "" {
				t.Fatalf("unsafe return path accepted: %d %s", w.Code, w.Header().Get("Location"))
			}
		})
	}
}
func TestConfidentialSecret(t *testing.T) {
	for _, method := range []string{"client_secret_post", "client_secret_basic"} {
		t.Run(method, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.id, f.secret = f.register(t, method, "<script>alert(1)</script>")
			v := f.tokenForm(f.code(t))
			if method == "client_secret_post" {
				v.Set("client_secret", "wrong")
			}
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/token", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if method == "client_secret_basic" {
				req.SetBasicAuth(f.id, "wrong")
			}
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != 401 {
				t.Fatal("wrong secret accepted")
			}
		})
	}
}
func TestHappyPaths(t *testing.T) {
	for _, oidcMode := range []bool{false, true} {
		t.Run(fmt.Sprint(oidcMode), func(t *testing.T) {
			login := "Alice"
			if oidcMode {
				login = "Alice@Example.COM"
			}
			f := newFixture(t, oidcMode, true, true, login)
			status, tokens := f.token(t, f.tokenForm(f.code(t)))
			if status != 200 {
				t.Fatal(tokens)
			}
			f.verify(t, tokens["access_token"].(string), true)
			status, newTokens := f.token(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {f.id}, "scope": {"ops:read"}})
			if status != 200 || newTokens["scope"] != "ops:read" {
				t.Fatal(newTokens)
			}
			f.verify(t, newTokens["access_token"].(string), true)
			r := f.request(t, "GET", "/private?hello=world", "", true)
			body, _ := io.ReadAll(r.Body)
			if r.StatusCode != 200 || !strings.Contains(string(body), "alice") {
				t.Fatalf("browser login: %d %s", r.StatusCode, body)
			}
		})
	}
}
func TestStatePersistence(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	path := filepath.Join(t.TempDir(), "state", "oauth.json")
	f.s.mu.Lock()
	f.s.cfg.StateFile = path
	f.s.mu.Unlock()
	_, tokens := f.token(t, f.tokenForm(f.code(t)))
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"access_token", "refresh_token"} {
		if strings.Contains(string(b), tokens[key].(string)) {
			t.Fatal("plaintext persisted")
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("state permissions")
	}
	s, err := newServer(f.s.cfg, f.web.URL, f.web.URL+"/mcp", f.web.transport)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Verify(context.Background(), tokens["access_token"].(string), nil); err != nil {
		t.Fatal(err)
	}
}

// testTransport dispatches HTTP messages through httptest without opening sockets.
// This keeps cookie/redirect/OAuth behavior intact in socket-restricted sandboxes.
type testTransport struct{ handlers map[string]http.Handler }

func (t *testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h := t.handlers[r.URL.Scheme+"://"+r.URL.Host]
	if h == nil {
		return nil, fmt.Errorf("unexpected test host: %s", r.URL.Host)
	}
	clone := r.Clone(r.Context())
	clone.RequestURI = r.URL.RequestURI()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, clone)
	response := w.Result()
	response.Request = r
	return response, nil
}

type testSite struct {
	URL       string
	transport http.RoundTripper
}

func (s *testSite) Client() *http.Client { return &http.Client{Transport: s.transport} }

func TestBrowserPagesEscaping(t *testing.T) {
	for _, tc := range []struct {
		name string
		data pageData
	}{
		{"consent", pageData{Name: "<script>client</script>", Host: "client.example", URI: "https://client.example/callback", Subject: "github:operator", CSRF: "test-csrf", Scopes: []string{"ops:read", "ops:write", "dev:run"}}},
		{"error", pageData{Error: "<script>error</script>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			htmlPage(w, 200, tc.data)
			body := w.Body.String()
			if strings.Contains(strings.ToLower(body), "<script") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, `class="brand"><img`) {
				t.Fatalf("unsafe or unstyled page: %s", body)
			}
			if tc.name == "consent" {
				for _, want := range []string{`class="host">client.example`, "unverified, self-reported", "github:operator", "Read server, Kubernetes and notes", "Change them after approval", "Start coding agents after approval", `class="chip">dev:run`} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %q", want)
					}
				}
			}
			if w.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("security headers changed")
			}
		})
	}
}

func TestAccessReturnsConfiguredRights(t *testing.T) {
	s := &Server{cfg: config.BuiltinAuth{Users: []config.User{{Subject: "alice", Scopes: []string{config.ScopeRead, config.ScopeWrite, "made:up"}, Tools: []string{"k8s_*"}}}}}
	scopes, tools, ok := s.Access("alice")
	if !ok || !slices.Equal(scopes, []string{config.ScopeRead, config.ScopeWrite}) || !slices.Equal(tools, []string{"k8s_*"}) {
		t.Fatalf("access = %v %v %v", scopes, tools, ok)
	}
	if _, _, ok := s.Access("mallory"); ok {
		t.Fatal("unknown user has access")
	}
}
