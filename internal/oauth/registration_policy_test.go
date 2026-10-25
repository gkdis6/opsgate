package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

func policyRegistration(t *testing.T, f *fixture, uris ...string) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"redirect_uris": uris, "client_name": "<script>client</script>"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/oauth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.register(w, req)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return w.Code, out
}

func TestRegistrationHostPolicy(t *testing.T) {
	for _, tt := range []struct {
		name          string
		allowed, uris []string
		mode          string
		status        int
		pending       bool
	}{
		{name: "empty allowlist", uris: []string{"https://any.example/cb"}, status: 201},
		{name: "exact case insensitive", allowed: []string{"CLIENT.EXAMPLE"}, uris: []string{"https://Client.Example:8443/cb"}, status: 201},
		{name: "default deny", allowed: []string{"safe.example"}, uris: []string{"https://other.example/cb"}, status: 400},
		{name: "explicit deny", allowed: []string{"safe.example"}, mode: "deny", uris: []string{"https://other.example/cb"}, status: 400},
		{name: "suffix denied", allowed: []string{"safe.example"}, uris: []string{"https://safe.example.evil/cb"}, status: 400},
		{name: "every URI checked", allowed: []string{"safe.example"}, uris: []string{"https://safe.example/cb", "https://evil.example/cb"}, status: 400},
		{name: "loopback family", allowed: []string{"LOCALHOST"}, uris: []string{"http://localhost:1234/cb", "http://127.5.6.7:4321/cb", "http://[::1]:6789/cb"}, status: 201},
		{name: "loopback lookalike", allowed: []string{"localhost"}, uris: []string{"https://localhost.evil/cb"}, status: 400},
		{name: "exact IPv6", allowed: []string{"[::1]"}, uris: []string{"http://[::1]:6789/cb"}, status: 201},
		{name: "private scheme", allowed: []string{"CURSOR:", "com.example.app:"}, uris: []string{"cursor://client/callback", "com.example.app:/cb"}, status: 201},
		{name: "private scheme needs entry", allowed: []string{"client"}, uris: []string{"cursor://client/callback"}, status: 400},
		{name: "private host cannot allow https", allowed: []string{"cursor:"}, uris: []string{"https://cursor/callback"}, status: 400},
		{name: "pending", allowed: []string{"safe.example"}, mode: "approve", uris: []string{"https://other.example/cb"}, status: 201, pending: true},
		{name: "approved policy still accepts known", allowed: []string{"safe.example"}, mode: "approve", uris: []string{"https://safe.example/cb"}, status: 201},
		{name: "invalid redirect cannot await approval", allowed: []string{"safe.example"}, mode: "approve", uris: []string{"http://evil.example/cb"}, status: 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.cfg.AllowedRedirectHosts, f.s.cfg.UnknownRedirectHosts = tt.allowed, tt.mode
			f.s.cfg.Admins = []string{"github:alice"}
			status, out := policyRegistration(t, f, tt.uris...)
			if status != tt.status {
				t.Fatalf("status=%d response=%v", status, out)
			}
			if status == 400 && out["error"] != "invalid_redirect_uri" {
				t.Fatal(out)
			}
			if status == 201 {
				f.s.mu.Lock()
				pending := f.s.clients[out["client_id"].(string)].Pending
				f.s.mu.Unlock()
				if pending != tt.pending {
					t.Fatalf("pending=%v", pending)
				}
			}
		})
	}
}

func TestRegistrationPolicyValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*config.BuiltinAuth)
		valid  bool
	}{
		{"defaults", func(*config.BuiltinAuth) {}, true},
		{"bad mode", func(b *config.BuiltinAuth) { b.UnknownRedirectHosts = "allow" }, false},
		{"approve needs admin", func(b *config.BuiltinAuth) { b.UnknownRedirectHosts = "approve" }, false},
		{"approve with admin", func(b *config.BuiltinAuth) { b.UnknownRedirectHosts = "approve"; b.Admins = []string{"github:alice"} }, true},
		{"empty admin", func(b *config.BuiltinAuth) { b.Admins = []string{""} }, false},
		{"unknown admin", func(b *config.BuiltinAuth) { b.Admins = []string{"github:bob"} }, false},
		{"admin case folding", func(b *config.BuiltinAuth) { b.Admins = []string{"github:ALICE"} }, true},
		{"immutable admin case", func(b *config.BuiltinAuth) {
			b.Users = []config.User{{Subject: "oidc:AbC"}}
			b.Admins = []string{"oidc:abc"}
		}, false},
		{"https notify", func(b *config.BuiltinAuth) { b.NotifyURL = "https://notify.example/hook?key=secret" }, true},
		{"http notify", func(b *config.BuiltinAuth) { b.NotifyURL = "http://notify.example/hook" }, false},
		{"loopback notify", func(b *config.BuiltinAuth) { b.NotifyURL = "http://127.9.8.7:1234/hook" }, true},
		{"IPv6 notify", func(b *config.BuiltinAuth) { b.NotifyURL = "http://[::1]/hook" }, true},
		{"localhost notify", func(b *config.BuiltinAuth) { b.NotifyURL = "http://LOCALHOST/hook" }, true},
		{"credential notify", func(b *config.BuiltinAuth) { b.NotifyURL = "https://user:secret@notify.example" }, false},
		{"fragment notify", func(b *config.BuiltinAuth) { b.NotifyURL = "https://notify.example/#" }, false},
		{"relative notify", func(b *config.BuiltinAuth) { b.NotifyURL = "/hook" }, false},
		{"exact hosts", func(b *config.BuiltinAuth) {
			b.AllowedRedirectHosts = []string{"EXAMPLE.com", "localhost", "127.1.2.3", "[::1]", "com.example.app:", "cursor:"}
		}, true},
		{"wildcard host", func(b *config.BuiltinAuth) { b.AllowedRedirectHosts = []string{"*.example.com"} }, false},
		{"URL host", func(b *config.BuiltinAuth) { b.AllowedRedirectHosts = []string{"https://example.com"} }, false},
		{"port host", func(b *config.BuiltinAuth) { b.AllowedRedirectHosts = []string{"example.com:1234"} }, false},
		{"empty host", func(b *config.BuiltinAuth) { b.AllowedRedirectHosts = []string{""} }, false},
		{"whitespace host", func(b *config.BuiltinAuth) { b.AllowedRedirectHosts = []string{" example.com"} }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Auth.Builtin.Users = []config.User{{Subject: "github:alice"}}
			tt.change(&cfg.Auth.Builtin)
			if err := cfg.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate=%v", err)
			}
			cfg.HTTP.PublicURL = "https://opsgate.test"
			cfg.Auth.Mode = config.AuthBuiltin
			if err := cfg.ValidateHTTP(); (err == nil) != tt.valid {
				t.Fatalf("ValidateHTTP=%v", err)
			}
		})
	}
}

func TestPendingClientCannotAuthorizeOrAuthenticate(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	f.s.cfg.AllowedRedirectHosts = []string{"safe.example"}
	f.s.cfg.UnknownRedirectHosts = "approve"
	_, out := policyRegistration(t, f, f.redirect)
	f.id = out["client_id"].(string)
	r := f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", false)
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(body), "This client is waiting for administrator approval") || r.Header.Get("Location") != "" {
		t.Fatalf("status=%d body=%s", r.StatusCode, body)
	}
	f.s.mu.Lock()
	started := len(f.s.pending) != 0
	f.s.mu.Unlock()
	if started {
		t.Fatal("pending client started upstream login")
	}
	status, _ := f.token(t, url.Values{"client_id": {f.id}, "grant_type": {"authorization_code"}, "code": {"fake"}})
	if status != 401 {
		t.Fatalf("pending client authenticated: %d", status)
	}
}

type noticeTransport func(*http.Request) (*http.Response, error)

func (f noticeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPendingClientNotification(t *testing.T) {
	for _, outcome := range []string{"success", "status failure", "transport failure", "cancelled", "redirect"} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.cfg.AllowedRedirectHosts = []string{"safe.example"}
			f.s.cfg.UnknownRedirectHosts = "approve"
			f.s.cfg.NotifyURL = "https://notify.example/hook?secret=webhook-secret"
			called := 0
			f.s.httpClient.Transport = noticeTransport(func(r *http.Request) (*http.Response, error) {
				called++
				// Notification I/O must not own the authorization lock.
				if !f.s.mu.TryLock() {
					t.Error("notification holds server lock")
				} else {
					f.s.mu.Unlock()
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("missing 5s timeout")
				}
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid webhook request")
				}
				var got clientNotice
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Event != "client_pending" || got.ClientID == "" || got.ClientName != "<script>client</script>" || strings.Join(got.RedirectHosts, ",") != "client.example,cursor:" || got.ApproveURL != f.web.URL+"/admin/clients" {
					t.Errorf("invalid notice: %+v", got)
				}
				switch outcome {
				case "transport failure":
					return nil, errors.New("webhook-secret")
				case "cancelled":
					return nil, context.Canceled
				case "status failure":
					return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("webhook-secret"))}, nil
				case "redirect":
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://foreign.example/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			var logs bytes.Buffer
			prior := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(prior)
			status, out := policyRegistration(t, f, f.redirect, "cursor://client/cb", f.redirect)
			if status != 201 || called != 1 {
				t.Fatalf("status=%d calls=%d response=%v", status, called, out)
			}
			if strings.Contains(logs.String(), "webhook-secret") {
				t.Fatal("webhook secret logged")
			}
			if (logs.Len() > 0) != (outcome != "success") {
				t.Fatalf("unexpected logs: %s", logs.String())
			}
		})
	}
}

func TestPendingClientEvictionAndPersistence(t *testing.T) {
	for _, test := range []string{"capacity", "expiry", "persistence"} {
		t.Run(test, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.cfg.AllowedRedirectHosts = []string{"client.example"}
			f.s.cfg.UnknownRedirectHosts = "approve"
			f.s.cfg.Admins = []string{"github:alice"}
			status, out := policyRegistration(t, f, "https://unknown.example/cb")
			if status != http.StatusCreated {
				t.Fatal(out)
			}
			id := out["client_id"].(string)
			switch test {
			case "capacity":
				f.s.mu.Lock()
				f.s.cfg.MaxClients = 2
				f.s.clients[f.id].Created = f.s.now().Add(-time.Hour)
				f.s.mu.Unlock()
				status, _ := policyRegistration(t, f, f.redirect)
				if status != 201 {
					t.Fatal(status)
				}
				f.s.mu.Lock()
				correct := len(f.s.clients) == 2 && f.s.clients[id] == nil && f.s.clients[f.id] != nil
				f.s.mu.Unlock()
				if !correct {
					t.Fatal("pending client was not evicted first")
				}
			case "expiry":
				f.s.mu.Lock()
				f.s.clients[id].Created = f.s.now().Add(-24 * time.Hour)
				f.s.sweep()
				exists := f.s.clients[id] != nil
				f.s.mu.Unlock()
				if exists {
					t.Fatal("pending client survived 24h")
				}
			case "persistence":
				f.s.mu.Lock()
				f.s.cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
				err := f.s.save()
				cfg := f.s.cfg
				// Only one instance should own a state file at a time.
				f.s.cfg.StateFile = ""
				f.s.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				restarted, err := newServer(cfg, f.s.issuer, f.s.resource, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := restarted.Close(); err != nil {
						t.Error(err)
					}
				})
				restarted.mu.Lock()
				pending := restarted.clients[id].Pending
				restarted.clients[id].Pending = false
				err = restarted.save()
				restarted.mu.Unlock()
				if !pending {
					t.Fatal("pending flag lost on restart")
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := restarted.Close(); err != nil {
					t.Fatal(err)
				}
				again, err := newServer(cfg, f.s.issuer, f.s.resource, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := again.Close(); err != nil {
						t.Error(err)
					}
				})
				again.mu.Lock()
				pending = again.clients[id].Pending
				again.mu.Unlock()
				if pending {
					t.Fatal("approval lost on restart")
				}
			}
		})
	}
}
