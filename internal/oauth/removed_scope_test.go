package oauth

import (
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestLoadStateDropsRemovedScope(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	code := f.code(t)
	status, tokens := f.token(t, f.tokenForm(code))
	if status != 200 {
		t.Fatalf("token: %d %v", status, tokens)
	}
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	for _, token := range f.s.access {
		token.Scopes = append(token.Scopes, "dev:run")
	}
	for _, token := range f.s.refresh {
		token.Scopes = append(token.Scopes, "dev:run")
	}
	for _, code := range f.s.codes {
		code.Scopes = append(code.Scopes, "dev:run")
	}
	if err := f.s.save(); err != nil {
		t.Fatal(err)
	}
	clientID := f.id
	if err := f.s.load(); err != nil {
		t.Fatal(err)
	}
	if f.s.clients[clientID] == nil || len(f.s.access) == 0 || len(f.s.refresh) == 0 || len(f.s.codes) == 0 {
		t.Fatal("state entries lost on load")
	}
	check := func(scopes []string) {
		t.Helper()
		if !slices.Equal(scopes, []string{"ops:read", "ops:write"}) {
			t.Fatalf("loaded scopes: %v", scopes)
		}
	}
	for _, token := range f.s.access {
		check(token.Scopes)
	}
	for _, token := range f.s.refresh {
		check(token.Scopes)
	}
	for _, code := range f.s.codes {
		check(code.Scopes)
	}
}

func TestRequestsDropRemovedScope(t *testing.T) {
	for _, scope := range []string{"ops:read dev:run", "dev:run"} {
		t.Run(scope, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			q := f.authQuery()
			q.Set("scope", scope)
			resp := f.request(t, "GET", "/oauth/authorize?"+q.Encode(), "", true)
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || strings.Contains(string(body), "dev:run") {
				t.Fatalf("authorize: %d %s", resp.StatusCode, body)
			}
			csrf := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindSubmatch(body)
			if len(csrf) != 2 {
				t.Fatal("missing CSRF")
			}
			resp = f.request(t, "POST", "/oauth/consent", url.Values{"csrf_token": {string(csrf[1])}, "decision": {"approve"}}.Encode(), false)
			resp.Body.Close()
			u, _ := url.Parse(resp.Header.Get("Location"))
			status, tokens := f.token(t, f.tokenForm(u.Query().Get("code")))
			want := strings.TrimSpace(strings.ReplaceAll(scope, "dev:run", ""))
			if status != 200 || tokens["scope"] != want {
				t.Fatalf("code exchange: %d %v", status, tokens)
			}
			status, tokens = f.token(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {f.id}, "refresh_token": {tokens["refresh_token"].(string)}, "scope": {scope}})
			if status != 200 || tokens["scope"] != want {
				t.Fatalf("refresh: %d %v", status, tokens)
			}
		})
	}
}
