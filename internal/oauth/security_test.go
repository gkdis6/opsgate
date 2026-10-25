package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/gkdis6/opsgate/internal/config"
)

func TestMetadata(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	r := f.request(t, "GET", "/.well-known/oauth-authorization-server", "", false)
	var got map[string]any
	if json.NewDecoder(r.Body).Decode(&got) != nil {
		t.Fatal("bad metadata")
	}
	for key, path := range map[string]string{"issuer": "", "authorization_endpoint": "/oauth/authorize", "token_endpoint": "/oauth/token", "registration_endpoint": "/oauth/register", "revocation_endpoint": "/oauth/revoke"} {
		if got[key] != f.web.URL+path {
			t.Fatalf("%s: %v", key, got[key])
		}
	}
	if got["authorization_response_iss_parameter_supported"] != true {
		t.Fatal("missing issuer binding")
	}
}
func TestRedirectAllowlist(t *testing.T) {
	for _, tt := range []struct {
		uri   string
		valid bool
	}{
		{"https://client.example/cb?a=b", true}, {"http://localhost:12345/cb", true}, {"http://127.0.0.1:1234/cb", true}, {"http://[::1]:123/cb", true}, {"com.example.app:/cb", true}, {"cursor://client/callback", true},
		{"ftp://client.example/cb", false}, {"http://localhost.evil/cb", false}, {"https://user:password@client.example/cb", false}, {"https://client.example/#", false}, {"https://client.example/" + strings.Repeat("x", 512), false}, {"http://127.0.0.2/cb", true}, {"https:opaque", false},
	} {
		t.Run(tt.uri, func(t *testing.T) {
			if got := validRedirect(tt.uri); got != tt.valid {
				t.Fatalf("validRedirect=%v", got)
			}
		})
	}
}
func TestRegistrationValidation(t *testing.T) {
	for _, body := range []string{
		`{"redirect_uris":["https://client.example"],"token_endpoint_auth_method":"client_secret_jwt"}`,
		`{"redirect_uris":["https://client.example"],"grant_types":["password"]}`,
		`{"redirect_uris":["https://client.example"],"response_types":["token"]}`,
		`{"redirect_uris":["https://client.example"],"client_name":"` + strings.Repeat("x", 101) + `"}`,
		`{"redirect_uris":["https://client.example"]} {}`,
		`{"redirect_uris":["https://client.example"],"grant_types":null}`,
		`{"redirect_uris":["https://client.example"],"response_types":null}`,
		`{"redirect_uris":["https://client.example"],"token_endpoint_auth_method":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/register", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != 400 {
				t.Fatal(r.Status)
			}
		})
	}
}
func TestRegistrationPruning(t *testing.T) {
	for _, neverUsed := range []bool{true, false} {
		t.Run(map[bool]string{true: "never used", false: "inactive"}[neverUsed], func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.mu.Lock()
			f.s.cfg.MaxClients = 1
			c := f.s.clients[f.id]
			c.Created = time.Now().Add(-25 * time.Hour)
			if !neverUsed {
				c.LastUsed = time.Now().Add(-91 * 24 * time.Hour)
			}
			f.s.mu.Unlock()
			id, _ := f.register(t, "none", "new")
			if id == f.id {
				t.Fatal("client ID collision")
			}
		})
	}
}
func TestRegistrationRateLimit(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	f.s.registrationLimit = rate.NewLimiter(0, 1)
	f.register(t, "none", "last")
	req, _ := http.NewRequest("POST", f.web.URL+"/oauth/register", strings.NewReader(`{"redirect_uris":["https://client.example"]}`))
	req.Header.Set("Content-Type", "application/json")
	r, err := f.browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 429 {
		t.Fatal(r.Status)
	}
}
func TestRequestBounds(t *testing.T) {
	for _, tt := range []struct{ path, body, contentType string }{
		{"/oauth/token", "client_id=" + strings.Repeat("x", bodyLimit), "application/x-www-form-urlencoded"},
		{"/oauth/revoke", "client_id=" + strings.Repeat("x", bodyLimit), "application/x-www-form-urlencoded"},
		{"/oauth/register", `{"client_name":"` + strings.Repeat("x", bodyLimit) + `"}`, "application/json"},
		{"/oauth/token", "grant_type=authorization_code&grant_type=refresh_token", "application/x-www-form-urlencoded"},
		{"/oauth/token", "{}", "application/json"},
	} {
		t.Run(tt.path+tt.contentType, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			req, _ := http.NewRequest("POST", f.web.URL+tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode < 400 {
				t.Fatal("invalid body accepted")
			}
		})
	}
}
func TestConfidentialHappyPaths(t *testing.T) {
	for _, method := range []string{"client_secret_post", "client_secret_basic"} {
		t.Run(method, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.id, f.secret = f.register(t, method, "<script>alert(1)</script>")
			v := f.tokenForm(f.code(t))
			if method == "client_secret_post" {
				v.Set("client_secret", f.secret)
			}
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/token", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if method == "client_secret_basic" {
				req.SetBasicAuth(f.id, f.secret)
			}
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			var got map[string]any
			json.NewDecoder(r.Body).Decode(&got)
			if r.StatusCode != 200 {
				t.Fatalf("%d %v", r.StatusCode, got)
			}
			f.verify(t, got["access_token"].(string), true)
			if f.s.clients[f.id].SecretHash == f.secret {
				t.Fatal("plaintext client secret retained")
			}
		})
	}
}
func TestConsentDenied(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	csrf := f.consent(t)
	r := f.request(t, "POST", "/oauth/consent", url.Values{"csrf_token": {csrf}, "decision": {"deny"}}.Encode(), false)
	u, _ := url.Parse(r.Header.Get("Location"))
	if u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Fatal("deny issued code")
	}
	r = f.request(t, "POST", "/oauth/consent", url.Values{"csrf_token": {csrf}, "decision": {"approve"}}.Encode(), false)
	if r.StatusCode < 400 {
		t.Fatal("consent reused")
	}
}
func TestConsentScopeIntersection(t *testing.T) {
	for _, requested := range []string{"", "ops:read", "ops:read ops:write"} {
		t.Run(requested, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.mu.Lock()
			f.s.cfg.Users[0].Scopes = []string{"ops:read"}
			f.s.mu.Unlock()
			v := f.authQuery()
			v.Set("scope", requested)
			r := f.request(t, "GET", "/oauth/authorize?"+v.Encode(), "", true)
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "ops:write") || !strings.Contains(string(body), "ops:read") {
				t.Fatal("consent scope escalation")
			}
		})
	}
}
func TestVerifyCurrentScopes(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	_, tokens := f.token(t, f.tokenForm(f.code(t)))
	f.s.mu.Lock()
	f.s.cfg.Users[0].Scopes = []string{"ops:read"}
	f.s.mu.Unlock()
	info, err := f.s.Verify(context.Background(), tokens["access_token"].(string), nil)
	if err != nil || strings.Join(info.Scopes, " ") != "ops:read" || info.UserID != "github:alice" || info.Extra["client_id"] != f.id {
		t.Fatalf("%+v %v", info, err)
	}
}
func TestRefreshResourceAndNarrowing(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	_, tokens := f.token(t, f.tokenForm(f.code(t)))
	v := url.Values{"grant_type": {"refresh_token"}, "client_id": {f.id}, "refresh_token": {tokens["refresh_token"].(string)}, "resource": {"https://evil.example/mcp"}}
	status, got := f.token(t, v)
	if status == 200 || got["error"] != "invalid_target" {
		t.Fatal(got)
	}
	v.Del("resource")
	v.Set("scope", "ops:read")
	status, got = f.token(t, v)
	if status != 200 {
		t.Fatal(got)
	}
	v.Set("refresh_token", got["refresh_token"].(string))
	v.Set("scope", "ops:write")
	status, got = f.token(t, v)
	if status == 200 || got["error"] != "invalid_scope" {
		t.Fatal("refresh restored dropped scope")
	}
}
func TestRefreshRevocation(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	_, tokens := f.token(t, f.tokenForm(f.code(t)))
	r := f.request(t, "POST", "/oauth/revoke", url.Values{"client_id": {f.id}, "token": {tokens["refresh_token"].(string)}}.Encode(), false)
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	f.verify(t, tokens["access_token"].(string), false)
	status, _ := f.token(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {f.id}, "refresh_token": {tokens["refresh_token"].(string)}})
	if status == 200 {
		t.Fatal("revoked refresh accepted")
	}
}
func TestConcurrentCodeRedemption(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	v := f.tokenForm(f.code(t))
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	access := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/token", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := f.browser.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			statuses <- resp.StatusCode
			var got map[string]any
			json.NewDecoder(resp.Body).Decode(&got)
			if token, ok := got["access_token"].(string); ok {
				access <- token
			}
		})
	}
	wg.Wait()
	close(statuses)
	close(access)
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful redemptions=%d", successes)
	}
	for token := range access {
		f.verify(t, token, false)
	}
}
func TestCookiePropertiesAndSessions(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	r := f.request(t, "GET", "/private?return=kept", "", false)
	cookies := r.Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing pending cookie")
	}
	c := cookies[0]
	if c.Name != pendingCookie || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatal("unsafe pending cookie")
	}
	r = f.request(t, "GET", "/private?return=kept", "", true)
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	u, _ := url.Parse(f.web.URL)
	for _, c := range f.browser.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			f.s.mu.Lock()
			f.s.sessions[hash(c.Value)].Expires = time.Now().Add(-time.Second)
			f.s.mu.Unlock()
		}
	}
	r = f.request(t, "GET", "/private", "", false)
	if r.StatusCode != 303 {
		t.Fatal("expired session accepted")
	}
}
func TestNewFailsClosed(t *testing.T) {
	t.Setenv("OG_TEST_SECRET", "secret")
	valid := config.Default().Auth.Builtin
	valid.Users = []config.User{{Subject: "github:alice", Scopes: []string{"ops:read"}}}
	valid.Upstream = config.Upstream{Provider: "github", ClientID: "id", ClientSecretEnv: "OG_TEST_SECRET"}
	for _, attack := range []string{"missing secret", "missing users", "http issuer", "bad resource", "bad state", "null state"} {
		t.Run(attack, func(t *testing.T) {
			cfg := valid
			issuer := "https://opsgate.test"
			resource := issuer + "/mcp"
			switch attack {
			case "missing secret":
				cfg.Upstream.ClientSecretEnv = "NONEXISTENT_OG_SECRET"
			case "missing users":
				cfg.Users = nil
			case "http issuer":
				issuer = "http://evil.example"
				resource = issuer + "/mcp"
			case "bad resource":
				resource = "https://other.example/mcp"
			case "bad state", "null state":
				cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
				body := "{}"
				if attack == "null state" {
					body = `{"version":1,"issuer":"https://opsgate.test","resource":"https://opsgate.test/mcp","clients":{"bad":null},"access":{},"refresh":{},"revoked_families":{},"codes":{}}`
				}
				if err := os.WriteFile(cfg.StateFile, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := New(cfg, issuer, resource)
			if err == nil {
				s.Close()
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
func TestPersistenceFailureFailsClosed(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	_, tokens := f.token(t, f.tokenForm(f.code(t)))
	f.s.mu.Lock()
	f.s.cfg.StateFile = filepath.Join(t.TempDir(), "not-a-dir", "state.json")
	if err := os.WriteFile(filepath.Dir(f.s.cfg.StateFile), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	f.s.mu.Unlock()
	r := f.request(t, "POST", "/oauth/revoke", url.Values{"client_id": {f.id}, "token": {tokens["access_token"].(string)}}.Encode(), false)
	if r.StatusCode != 503 {
		t.Fatal("failed persistence reported success")
	}
	f.verify(t, tokens["access_token"].(string), false)
	f.closeErrorExpected = true
	if f.s.Close() == nil {
		t.Fatal("Close hid storage failure")
	}

}
func TestReplaySurvivesRestart(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	f.s.mu.Lock()
	f.s.cfg.StateFile = filepath.Join(t.TempDir(), "state", "oauth.json")
	f.s.mu.Unlock()
	code := f.code(t)
	_, tokens := f.token(t, f.tokenForm(code))
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := newServer(f.s.cfg, f.web.URL, f.web.URL+"/mcp", f.web.transport)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	s.Routes(mux)
	req := httptest.NewRequest("POST", f.web.URL+"/oauth/token", strings.NewReader(f.tokenForm(code).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("replayed code status=%d", w.Code)
	}
	if _, err := s.Verify(context.Background(), tokens["access_token"].(string), nil); err == nil {
		t.Fatal("replay across restart preserved access")
	}
}

func TestClientAuthenticationAttacks(t *testing.T) {
	for _, attack := range []string{"unknown client", "wrong auth method", "multiple auth methods", "mismatched client IDs"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			if attack != "unknown client" {
				f.id, f.secret = f.register(t, "client_secret_basic", "<script>alert(1)</script>")
			}
			v := f.tokenForm(f.code(t))
			basicID, basicSecret := "", ""
			switch attack {
			case "unknown client":
				v.Set("client_id", "unknown")
			case "wrong auth method":
				v.Set("client_secret", f.secret)
			case "multiple auth methods":
				v.Set("client_secret", "")
				basicID, basicSecret = f.id, f.secret
			case "mismatched client IDs":
				basicID, basicSecret = f.id, f.secret
				v.Set("client_id", "other")
			}
			req, _ := http.NewRequest("POST", f.web.URL+"/oauth/token", strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if basicID != "" {
				req.SetBasicAuth(basicID, basicSecret)
			}
			r, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			if r.StatusCode != 401 {
				t.Fatal("invalid client authentication accepted")
			}
		})
	}
}

func TestMissingTokenParameters(t *testing.T) {
	for _, grant := range []string{"", "authorization_code", "refresh_token"} {
		t.Run(grant, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			status, got := f.token(t, url.Values{"client_id": {f.id}, "grant_type": {grant}})
			if status != 400 || got["error"] != "invalid_request" {
				t.Fatalf("%d %v", status, got)
			}
		})
	}
}
