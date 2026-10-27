package oauth

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func adminForm(t *testing.T, f *fixture) string {
	t.Helper()
	resp := f.request(t, "GET", "/admin/clients", "", true)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("admin page: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'; form-action 'self'" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("missing admin page protection")
	}
	if strings.Contains(string(body), "<script>") || !strings.Contains(string(body), "&lt;script&gt;") {
		t.Fatalf("client name not escaped: %s", body)
	}
	match := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("missing CSRF: %s", body)
	}
	return string(match[1])
}

func TestAdminClientDecisions(t *testing.T) {
	for _, action := range []string{"approve", "reject", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.cfg.Admins = []string{"github:alice"}
			f.s.cfg.AllowedRedirectHosts = []string{"safe.example"}
			f.s.cfg.UnknownRedirectHosts = "approve"
			_, out := policyRegistration(t, f, f.redirect)
			id := out["client_id"].(string)
			csrf := adminForm(t, f)
			form := url.Values{"csrf": {csrf}, "client_id": {id}, "action": {action}}
			r := f.request(t, "POST", "/admin/clients", form.Encode(), false)
			if r.StatusCode != 303 || r.Header.Get("Location") != "/admin/clients" {
				t.Fatalf("action failed: %d", r.StatusCode)
			}
			f.s.mu.Lock()
			c := f.s.clients[id]
			f.s.mu.Unlock()
			if action == "approve" {
				if c == nil || c.Pending {
					t.Fatal("client not approved")
				}
				f.id = id
				r = f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", false)
				if r.StatusCode != 303 {
					t.Fatal("approved client could not authorize")
				}
			} else if c != nil {
				t.Fatal("client not deleted")
			}
			// A successful mutation consumes the token even for a different client.
			form.Set("client_id", f.id)
			r = f.request(t, "POST", "/admin/clients", form.Encode(), false)
			if r.StatusCode != 403 {
				t.Fatal("CSRF replay accepted")
			}
		})
	}
}

func TestAdminClientAccessAndCSRF(t *testing.T) {
	for _, attack := range []string{"nonadmin GET", "nonadmin POST", "missing token", "wrong token", "duplicate token", "extra field", "cross origin", "fetch cross site", "other session", "unknown client", "bad action", "large body"} {
		t.Run(attack, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.cfg.Admins = []string{"github:alice"}
			csrf := adminForm(t, f)
			form := url.Values{"csrf": {csrf}, "client_id": {f.id}, "action": {"delete"}}
			method := "POST"
			want := 403
			switch attack {
			case "nonadmin GET":
				method = "GET"
				f.s.cfg.Admins = nil
			case "nonadmin POST":
				f.s.cfg.Admins = nil
			case "missing token":
				form.Del("csrf")
			case "wrong token":
				form.Set("csrf", "wrong")
			case "duplicate token":
				form.Add("csrf", csrf)
			case "extra field":
				form.Set("extra", "bad")
			case "unknown client":
				form.Set("client_id", "unknown")
				want = 404
			case "bad action":
				form.Set("action", "unknown")
				want = 400
			case "large body":
				form.Set("csrf", strings.Repeat("x", 5000))
				want = 400
			case "other session":
				raw := random()
				f.s.mu.Lock()
				f.s.sessions[hash(raw)] = &browserSession{Subject: "github:alice", Expires: time.Now().Add(time.Hour)}
				f.s.mu.Unlock()
				u, _ := url.Parse(f.web.URL)
				f.browser.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: raw, Path: "/", Secure: true}})
			}
			req, _ := http.NewRequest(method, f.web.URL+"/admin/clients", strings.NewReader(form.Encode()))
			if method == "GET" {
				req.Body = nil
				req.ContentLength = 0
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if attack == "cross origin" {
				req.Header.Set("Origin", "https://evil.example")
			}
			if attack == "fetch cross site" {
				req.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			resp, err := f.browser.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != want {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, want, body)
			}
			f.s.mu.Lock()
			exists := f.s.clients[f.id] != nil
			f.s.mu.Unlock()
			if !exists {
				t.Fatal("attack deleted client")
			}
		})
	}
}

func TestAdminDeleteRevokesCredentials(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	status, tokens := f.token(t, f.tokenForm(f.code(t)))
	if status != 200 {
		t.Fatal(tokens)
	}
	code := f.code(t)
	f.s.cfg.Admins = []string{"github:alice"}
	csrf := adminForm(t, f)
	r := f.request(t, "POST", "/admin/clients", url.Values{"csrf": {csrf}, "client_id": {f.id}, "action": {"delete"}}.Encode(), false)
	if r.StatusCode != 303 {
		t.Fatal(r.Status)
	}
	f.verify(t, tokens["access_token"].(string), false)
	status, _ = f.token(t, url.Values{"client_id": {f.id}, "grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}})
	if status != 401 {
		t.Fatal("deleted client refreshed token")
	}
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if len(f.s.access) != 0 || len(f.s.refresh) != 0 || f.s.codes[hash(code)] != nil || len(f.s.pending) != 0 {
		t.Fatal("client credentials retained")
	}
}
