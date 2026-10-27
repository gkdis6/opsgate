package oauth

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnchangedRequestsDoNotWriteState(t *testing.T) {
	for _, kind := range []string{"unknown revocation", "other client revocation", "repeated access revocation", "repeated refresh revocation", "repeated code replay", "bad code", "bad refresh", "invalid registration", "full registration", "authorize"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			code := f.code(t)
			_, tokens := f.token(t, f.tokenForm(code))
			f.s.mu.Lock()
			f.s.cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
			f.s.mu.Unlock()
			path, body, status := "/oauth/revoke", url.Values{"client_id": {f.id}, "token": {"unknown"}}.Encode(), 200
			switch kind {
			case "other client revocation":
				id, _ := f.register(t, "none", "other")
				body = url.Values{"client_id": {id}, "token": {tokens["access_token"].(string)}}.Encode()
			case "repeated access revocation", "repeated refresh revocation":
				key := "access_token"
				if kind == "repeated refresh revocation" {
					key = "refresh_token"
				}
				body = url.Values{"client_id": {f.id}, "token": {tokens[key].(string)}}.Encode()
				r := f.request(t, "POST", path, body, false)
				r.Body.Close()
				if r.StatusCode != 200 {
					t.Fatalf("first revocation: %d", r.StatusCode)
				}
			case "repeated code replay":
				path, body, status = "/oauth/token", f.tokenForm(code).Encode(), 400
				r := f.request(t, "POST", path, body, false)
				r.Body.Close()
				if r.StatusCode != 400 {
					t.Fatalf("first replay: %d", r.StatusCode)
				}
			case "bad code":
				path, body, status = "/oauth/token", f.tokenForm("unknown").Encode(), 400
			case "bad refresh":
				path, body, status = "/oauth/token", url.Values{"client_id": {f.id}, "grant_type": {"refresh_token"}, "refresh_token": {"unknown"}}.Encode(), 400
			case "invalid registration", "full registration":
				path, body, status = "/oauth/register", `{}`, 400
				if kind == "full registration" {
					f.s.mu.Lock()
					f.s.cfg.MaxClients = len(f.s.clients)
					f.s.mu.Unlock()
					body = `{"redirect_uris":["https://client.example/new"]}`
				}
			case "authorize":
				path, body, status = "/oauth/authorize?"+f.authQuery().Encode(), "", 303
			}
			f.s.mu.Lock()
			err := f.s.save()
			f.s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(f.s.cfg.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			method := "POST"
			if kind == "authorize" {
				method = "GET"
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if strings.Contains(kind, "registration") {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			mux := http.NewServeMux()
			f.s.Routes(mux)
			mux.ServeHTTP(w, r)
			if w.Code != status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			after, err := os.Stat(f.s.cfg.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) {
				t.Fatal("unchanged request replaced state file")
			}
		})
	}
}

func TestFullStatePoolsLoad(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.s.cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	expires := time.Now().Add(time.Hour)
	for i := range maxTokens {
		key := hash(fmt.Sprint(i))
		f.s.families[key[:43]] = expires
		if i < maxTokens/2 {
			token := accessToken{ClientID: f.id, Subject: "github:alice", Family: key[:43], Scopes: []string{"ops:read", "ops:write", "dev:run"}, Expires: expires}
			f.s.access[key] = &token
			f.s.refresh[key] = &refreshToken{accessToken: token, Used: true, RetainUntil: expires}
		}
		f.s.codes[key] = &authorizationCode{ClientID: f.id, RedirectURI: "https://client.example/" + strings.Repeat("&", 490), Challenge: strings.Repeat("x", 128), Subject: "github:alice", Resource: f.s.resource, Family: key[:43], Scopes: []string{"ops:read", "ops:write", "dev:run"}, Expires: expires, RetainUntil: expires, Used: true}
	}
	if err := f.s.save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.s.cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 64<<20 || info.Size() > maxStateBytes {
		t.Fatalf("state size=%d", info.Size())
	}
	f.s.codes, f.s.access, f.s.refresh, f.s.families = nil, nil, nil, nil
	if err := f.s.load(); err != nil {
		t.Fatal(err)
	}
	if len(f.s.codes) != maxTokens || len(f.s.access)+len(f.s.refresh) != maxTokens || len(f.s.families) != maxTokens {
		t.Fatalf("loaded codes=%d access=%d refresh=%d families=%d", len(f.s.codes), len(f.s.access), len(f.s.refresh), len(f.s.families))
	}
}

func TestStateWriterLimit(t *testing.T) {
	var output bytes.Buffer
	w := &stateWriter{Writer: &output, remaining: 4}
	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("5")); err == nil || output.String() != "1234" {
		t.Fatalf("oversized write: output=%q error=%v", output.String(), err)
	}
}

func TestSweepReportsOnlyDurableChanges(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	f.s.pending["expired"] = &pendingAuth{Expires: time.Time{}}
	if f.s.sweep() {
		t.Fatal("in-memory expiry reported a durable change")
	}
	f.s.access["expired"] = &accessToken{}
	if !f.s.sweep() || f.s.sweep() {
		t.Fatal("durable expiry was not reported exactly once")
	}
}
