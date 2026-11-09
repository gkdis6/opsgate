package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/time/rate"

	"github.com/gkdis6/opsgate/internal/config"
)

// fuzzServer is a passkey server without persistence or network access.
func fuzzServer(f *testing.F) (*Server, *http.ServeMux, string) {
	cfg := config.Default().Auth.Builtin
	cfg.Users = []config.User{{Subject: "passkey:alice", Scopes: []string{"ops:read", "ops:write"}}}
	cfg.Upstream = config.Upstream{Provider: "passkey"}
	cfg.StateFile = ""
	s, err := newServer(cfg, "https://ops.example", "https://ops.example/mcp", nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = s.Close() })
	s.registrationLimit = rate.NewLimiter(rate.Inf, 1)
	mux := http.NewServeMux()
	s.Routes(mux)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"redirect_uris":["https://client.example/cb"],"client_name":"fuzz"}`))
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)
	var got struct {
		ID string `json:"client_id"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		f.Fatalf("register: %d %s", w.Code, w.Body)
	}
	return s, mux, got.ID
}

func FuzzValidRedirect(f *testing.F) {
	for _, seed := range []string{"https://client.example/cb", "http://127.0.0.1:8/cb", "com.example.app:/cb", "cursor://x", "JavaScript:alert(1)", "http://localhost.evil/", "https://u@h/", "HTTP://[::1]/", "data:text/html,x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if !validRedirect(raw) {
			return
		}
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "#\\\r\n\t") || len(raw) > 512 {
			t.Fatalf("accepted %q", raw)
		}
		switch u.Scheme {
		case "javascript", "data", "file", "vbscript":
			t.Fatalf("accepted script scheme %q", raw)
		case "http":
			if !loopback(u.Hostname()) {
				t.Fatalf("accepted remote http %q", raw)
			}
		case "https":
			if u.Hostname() == "" {
				t.Fatalf("accepted https without host %q", raw)
			}
		default:
			if !strings.Contains(u.Scheme, ".") && u.Scheme != "cursor" {
				t.Fatalf("accepted scheme %q", raw)
			}
		}
	})
}

func FuzzRegister(f *testing.F) {
	for _, seed := range []string{
		`{"redirect_uris":["https://client.example/cb"]}`,
		`{"redirect_uris":["http://127.0.0.1/cb"],"token_endpoint_auth_method":"client_secret_post","grant_types":["authorization_code"]}`,
		`{"redirect_uris":["https://a/","https://b/"],"client_name":"\u0000x","response_types":["code"]}`,
		`{"redirect_uris":null}`, `[]`, `{"redirect_uris":["javascript:x"]}`,
	} {
		f.Add(seed)
	}
	s, mux, _ := fuzzServer(f)
	f.Fuzz(func(t *testing.T, body string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Fatalf("server error %d for %q", w.Code, body)
		}
		if w.Code != http.StatusCreated {
			return
		}
		var got struct {
			ID      string   `json:"client_id"`
			URIs    []string `json:"redirect_uris"`
			Method  string   `json:"token_endpoint_auth_method"`
			Grants  []string `json:"grant_types"`
			Secret  string   `json:"client_secret"`
			Name    string   `json:"client_name"`
			Unknown any      `json:"scope"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.URIs) == 0 || len(got.URIs) > 5 || got.Unknown != nil {
			t.Fatalf("registered %q", body)
		}
		for _, uri := range got.URIs {
			if !validRedirect(uri) {
				t.Fatalf("registered invalid redirect %q", uri)
			}
		}
		for _, g := range got.Grants {
			if g != "authorization_code" && g != "refresh_token" {
				t.Fatalf("registered grant %q", g)
			}
		}
		if (got.Method == "none") != (got.Secret == "") {
			t.Fatalf("method %q with secret %t", got.Method, got.Secret != "")
		}
		s.mu.Lock()
		c := s.clients[got.ID]
		if c == nil || c.SecretHash != "" && c.SecretHash == got.Secret {
			s.mu.Unlock()
			t.Fatal("client not stored or secret stored in clear")
		}
		delete(s.clients, got.ID)
		s.mu.Unlock()
	})
}

// No query, however malformed, may reach the passkey page without a valid
// client, redirect and PKCE challenge, or grow the pending pools past their caps.
func FuzzAuthorize(f *testing.F) {
	s, mux, id := fuzzServer(f)
	good := url.Values{"client_id": {id}, "redirect_uri": {"https://client.example/cb"}, "response_type": {"code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {"S256"}, "scope": {"ops:read"}, "state": {"s"}}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/oauth/authorize?"+good.Encode(), nil))
	if w.Code != http.StatusSeeOther {
		f.Fatalf("valid authorize: %d %s", w.Code, w.Body)
	}
	f.Add(good.Encode())
	f.Add(strings.Replace(good.Encode(), "S256", "plain", 1))
	f.Add(good.Encode() + "&client_id=x")
	f.Add("client_id=" + id + "&redirect_uri=https%3A%2F%2Fclient.example%2Fcb%2F..")
	f.Fuzz(func(t *testing.T, raw string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/oauth/authorize", nil)
		r.URL.RawQuery = raw
		mux.ServeHTTP(w, r)
		if w.Code >= 500 {
			t.Fatalf("server error %d for %q", w.Code, raw)
		}
		if w.Code == http.StatusSeeOther || w.Code == http.StatusFound {
			v, err := url.ParseQuery(raw)
			if err != nil || v.Get("client_id") != id || v.Get("redirect_uri") != "https://client.example/cb" || v.Get("code_challenge_method") != "S256" || !pkceSyntax(v.Get("code_challenge")) || v.Get("response_type") != "code" {
				t.Fatalf("started sign-in for %q", raw)
			}
			if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/passkey/login") && !strings.HasPrefix(loc, "/oauth/passkey") {
				t.Fatalf("redirected to %q", loc)
			}
		}
		s.mu.Lock()
		n := len(s.pending)
		s.mu.Unlock()
		if n > maxPending+maxLoginPending {
			t.Fatalf("pending grew to %d", n)
		}
	})
}

// Without a code or refresh token issued by this server, no form may obtain
// a token.
func FuzzToken(f *testing.F) {
	_, mux, id := fuzzServer(f)
	for _, seed := range []string{
		"grant_type=authorization_code&code=x&client_id=" + id + "&redirect_uri=https%3A%2F%2Fclient.example%2Fcb&code_verifier=" + strings.Repeat("a", 43),
		"grant_type=refresh_token&refresh_token=og_rt_x&client_id=" + id,
		"grant_type=client_credentials", "grant_type=password&username=a&password=b", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, form string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusOK || w.Code >= 500 {
			t.Fatalf("token endpoint %d for %q: %s", w.Code, form, w.Body)
		}
		if strings.Contains(w.Body.String(), "access_token") {
			t.Fatalf("token issued for %q", form)
		}
	})
}
