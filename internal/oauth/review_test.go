package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

type blockedWriter struct {
	http.ResponseWriter
	started, release chan struct{}
	once             sync.Once
	blockHeader      bool
}

func (w *blockedWriter) block() {
	w.once.Do(func() { close(w.started) })
	<-w.release
}
func (w *blockedWriter) Write(p []byte) (int, error) {
	w.block()
	return w.ResponseWriter.Write(p)
}
func (w *blockedWriter) WriteHeader(status int) {
	if w.blockHeader {
		w.block()
	}
	w.ResponseWriter.WriteHeader(status)
}

func TestBlockedOAuthResponseDoesNotBlockVerify(t *testing.T) {
	for _, route := range []string{"authorize", "authorize error", "callback", "consent", "register", "token", "refresh", "revoke", "login"} {
		t.Run(route, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			path, body, method := "/oauth/authorize?"+f.authQuery().Encode(), "", "GET"
			switch route {
			case "authorize error":
				v := f.authQuery()
				v.Set("scope", "invalid")
				path = "/oauth/authorize?" + v.Encode()
			case "callback":
				auth := f.request(t, "GET", path, "", false)
				browser := *f.browser
				browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
				upstream, err := browser.Get(auth.Header.Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				defer upstream.Body.Close()
				path = strings.TrimPrefix(upstream.Header.Get("Location"), f.web.URL)
			case "consent":
				csrf := f.consent(t)
				path, method, body = "/oauth/consent", "POST", url.Values{"csrf_token": {csrf}, "decision": {"approve"}}.Encode()
			case "register":
				path, method, body = "/oauth/register", "POST", `{"redirect_uris":["https://client.example/new"]}`
			case "token":
				path, method, body = "/oauth/token", "POST", f.tokenForm(f.code(t)).Encode()
			case "refresh":
				_, tokens := f.token(t, f.tokenForm(f.code(t)))
				path, method, body = "/oauth/token", "POST", url.Values{"client_id": {f.id}, "grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}}.Encode()
			case "revoke":
				path, method, body = "/oauth/revoke", "POST", url.Values{"client_id": {f.id}, "token": {"unknown"}}.Encode()
			case "login":
				path = "/private"
			}
			r := httptest.NewRequest(method, f.web.URL+path, strings.NewReader(body))
			if method == "POST" {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if route == "register" {
				r.Header.Set("Content-Type", "application/json")
			}
			for _, c := range f.browser.Jar.Cookies(r.URL) {
				r.AddCookie(c)
			}
			f.s.mu.Lock()
			f.s.access[hash("og_at_probe")] = &accessToken{ClientID: f.id, Subject: "github:alice", Scopes: []string{"ops:read"}, Expires: time.Now().Add(time.Hour)}
			f.s.mu.Unlock()
			w := &blockedWriter{ResponseWriter: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), blockHeader: route == "revoke" || route == "consent"}
			done := make(chan struct{})
			go func() { defer close(done); f.web.transport.(*testTransport).handlers[f.web.URL].ServeHTTP(w, r) }()
			defer func() { close(w.release); <-done }()
			select {
			case <-w.started:
			case <-time.After(time.Second):
				t.Fatal("handler did not start writing")
			}
			verified := make(chan error, 1)
			go func() { _, err := f.s.Verify(context.Background(), "og_at_probe", nil); verified <- err }()
			select {
			case err := <-verified:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("response write held the OAuth mutex")
			}
		})
	}
}

func TestOAuthGETBodiesRejectedBeforeLock(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	mux := http.NewServeMux()
	f.s.Routes(mux)
	for _, path := range []string{"/oauth/authorize?" + f.authQuery().Encode(), "/oauth/callback", "/.well-known/oauth-authorization-server"} {
		for _, chunked := range []bool{false, true} {
			r := httptest.NewRequest("GET", path, nil)
			r.ContentLength = 1
			if chunked {
				r.ContentLength = -1
				r.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			done := make(chan struct{})
			f.s.mu.Lock()
			go func() { defer close(done); mux.ServeHTTP(w, r) }()
			select {
			case <-done:
				f.s.mu.Unlock()
			case <-time.After(500 * time.Millisecond):
				f.s.mu.Unlock()
				<-done
				t.Fatal("GET body rejection waited on OAuth mutex")
			}
			if w.Code != 400 {
				t.Fatalf("%s chunked=%v status=%d", path, chunked, w.Code)
			}
		}
	}
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineWriter) SetReadDeadline(deadline time.Time) error { w.deadline = deadline; return nil }

type deadlineBody struct {
	t *testing.T
	w *deadlineWriter
}

func (b deadlineBody) Read([]byte) (int, error) {
	if b.w.deadline.IsZero() {
		b.t.Error("POST body read without deadline")
	}
	return 0, io.EOF
}
func (deadlineBody) Close() error { return nil }

func TestOAuthPOSTReadDeadline(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	mux := http.NewServeMux()
	f.s.Routes(mux)
	for _, path := range []string{"/oauth/register", "/oauth/token", "/oauth/revoke", "/oauth/consent"} {
		w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		r := httptest.NewRequest("POST", path, nil)
		r.Body = deadlineBody{t, w}
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if path == "/oauth/register" {
			r.Header.Set("Content-Type", "application/json")
		}
		before := time.Now()
		mux.ServeHTTP(w, r)
		if w.deadline.Before(before.Add(9*time.Second)) || w.deadline.After(time.Now().Add(10*time.Second)) {
			t.Fatalf("%s deadline=%v", path, w.deadline)
		}
	}
}

func TestOIDCSubjectOwnership(t *testing.T) {
	for _, tt := range []struct {
		name, issuer, email, hd, allow, sub string
		verified, omitEmail, allowed        bool
	}{
		{name: "Google external mailbox", issuer: "https://accounts.google.com", email: "alice@example.com", verified: true},
		{name: "Google Gmail", issuer: "https://accounts.google.com", email: "Alice@Gmail.COM", verified: true, allowed: true},
		{name: "Google Googlemail", issuer: "https://accounts.google.com", email: "alice@googlemail.com", verified: true, allowed: true},
		{name: "Google Workspace", issuer: "https://accounts.google.com", email: "alice@example.com", hd: "example.com", verified: true, allowed: true},
		{name: "Google wrong hosted domain", issuer: "https://accounts.google.com", email: "alice@example.com", hd: "other.com", verified: true},
		{name: "Google unverified Gmail", issuer: "https://accounts.google.com", email: "alice@gmail.com"},
		{name: "other issuer email", issuer: "https://upstream.test", email: "alice@example.com", verified: true, allowed: true},
		{name: "Google immutable", issuer: "https://accounts.google.com", email: "alice@example.com", allow: "oidc:123", verified: true, allowed: true},
		{name: "immutable without email", issuer: "https://upstream.test", allow: "oidc:123", omitEmail: true, allowed: true},
		{name: "immutable unverified email", issuer: "https://upstream.test", email: "alice@example.com", allow: "oidc:123", allowed: true},
		{name: "immutable case sensitive", issuer: "https://upstream.test", sub: "AbC", allow: "oidc:abc", omitEmail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixtureIdentity(t, true, tt.verified, true, tt.email, fixtureIdentity{issuer: tt.issuer, subject: tt.sub, hostedDomain: tt.hd, omitEmail: tt.omitEmail})
			subject := tt.allow
			if subject == "" {
				subject = "email:" + strings.ToLower(tt.email)
			}
			f.s.mu.Lock()
			f.s.cfg.Users = []config.User{{Subject: subject, Scopes: []string{"ops:read"}}}
			f.s.mu.Unlock()
			r := f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", true)
			body, _ := io.ReadAll(r.Body)
			if !tt.allowed {
				if r.StatusCode != 403 || r.Header.Get("Location") != "" {
					t.Fatalf("untrusted subject accepted: %d %s", r.StatusCode, body)
				}
				return
			}
			if r.StatusCode != 200 || !strings.Contains(string(body), "<dt>Signed in as</dt><dd>"+subject+"</dd>") {
				t.Fatalf("subject rejected: %d %s", r.StatusCode, body)
			}
			if strings.HasPrefix(subject, "oidc:") {
				f.s.mu.Lock()
				_, matched := f.s.user(strings.ToUpper(subject))
				f.s.mu.Unlock()
				if matched {
					t.Fatal("immutable subjects matched without case sensitivity")
				}
			}
		})
	}
}

func TestRegistrationCapacityEvictsOnlyOldUnusedClients(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	other, _ := f.register(t, "none", "other")
	f.s.mu.Lock()
	f.s.cfg.MaxClients = 2
	f.s.clients[f.id].Created = time.Now().Add(-20 * time.Minute)
	f.s.clients[other].Created = time.Now().Add(-15 * time.Minute)
	f.s.mu.Unlock()
	newID, _ := f.register(t, "none", "new")
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	if len(f.s.clients) != 2 || f.s.clients[f.id] != nil || f.s.clients[other] == nil || f.s.clients[newID] == nil {
		t.Fatal("capacity did not evict the oldest unused client")
	}
}

func TestRegistrationCapacityProtectsActiveClients(t *testing.T) {
	for _, kind := range []string{"recent", "issued token", "access", "refresh", "pending", "code"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.mu.Lock()
			f.s.cfg.MaxClients = 1
			f.s.clients[f.id].Created = time.Now().Add(-20 * time.Minute)
			expires := time.Now().Add(time.Hour)
			switch kind {
			case "recent":
				f.s.clients[f.id].Created = time.Now().Add(-9 * time.Minute)
			case "issued token":
				f.s.clients[f.id].LastUsed = time.Now().Add(-time.Minute)
			case "access":
				f.s.access["a"] = &accessToken{ClientID: f.id, Expires: expires}
			case "refresh":
				f.s.refresh["r"] = &refreshToken{accessToken: accessToken{ClientID: f.id, Expires: expires}, RetainUntil: expires}
			case "pending":
				f.s.pending["p"] = &pendingAuth{ClientID: f.id, Kind: "authorize", Expires: expires}
			case "code":
				f.s.codes["c"] = &authorizationCode{ClientID: f.id, Expires: expires, RetainUntil: expires}
			}
			f.s.mu.Unlock()
			r := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"redirect_uris":["https://client.example/new"]}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.s.register(w, r)
			f.s.mu.Lock()
			defer f.s.mu.Unlock()
			if w.Code != 400 || f.s.clients[f.id] == nil {
				t.Fatalf("protected client evicted: status=%d", w.Code)
			}
		})
	}
}

func TestPendingAuthorizationEviction(t *testing.T) {
	for _, pool := range []string{"client", "global", "login isolated", "login full"} {
		t.Run(pool, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			f.s.mu.Lock()
			oldest := time.Now().Add(time.Minute)
			// Every pool has an older entry belonging to the other pool. It must
			// survive eviction even when the pool under test has reached its cap.
			otherKind := "login"
			if pool == "login full" {
				otherKind = "authorize"
			}
			f.s.pending["other-pool"] = &pendingAuth{Kind: otherKind, ClientID: "other-pool", Expires: oldest.Add(-time.Second)}
			if pool == "client" {
				f.s.pending["other-client"] = &pendingAuth{Kind: "authorize", ClientID: "other-client", Expires: oldest.Add(-time.Second)}
			}
			limit := 20
			if pool == "global" || pool == "login isolated" {
				limit = maxPending
			}
			if pool == "login full" {
				limit = maxLoginPending
			}
			for i := range limit {
				key := strings.Repeat("x", i+1)
				id, kind := f.id, "authorize"
				if pool != "client" {
					id = key
				}
				if pool == "login full" {
					kind = "login"
				}
				f.s.pending[key] = &pendingAuth{Kind: kind, ClientID: id, Expires: oldest.Add(time.Duration(i) * time.Millisecond)}
			}
			f.s.mu.Unlock()
			path := "/oauth/authorize?" + f.authQuery().Encode()
			if pool == "login isolated" || pool == "login full" {
				path = "/private"
			}
			r := f.request(t, "GET", path, "", false)
			status := http.StatusSeeOther
			if pool == "login full" {
				status = http.StatusTooManyRequests
			}
			if r.StatusCode != status {
				t.Fatalf("full pending pool: status=%d want=%d", r.StatusCode, status)
			}
			f.s.mu.Lock()
			defer f.s.mu.Unlock()
			want := limit + 1
			if pool == "client" || pool == "login isolated" {
				want++
			}
			if len(f.s.pending) != want {
				t.Fatalf("pending=%d want=%d", len(f.s.pending), want)
			}
			if f.s.pending["other-pool"] == nil || pool == "client" && f.s.pending["other-client"] == nil {
				t.Fatal("evicted from unrelated pool")
			}
			if (f.s.pending["x"] != nil) != (pool == "login isolated" || pool == "login full") {
				t.Fatal("incorrect oldest-entry eviction")
			}
		})
	}
}

func TestPreConsentServerErrorsStayLocal(t *testing.T) {
	for _, kind := range []string{"closed", "unauthorized client"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, false, true, true, "Alice")
			if kind == "closed" {
				if err := f.s.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				f.s.mu.Lock()
				f.s.clients[f.id].GrantTypes = []string{"refresh_token"}
				f.s.mu.Unlock()
			}
			r := f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", false)
			if r.StatusCode < 400 || r.Header.Get("Location") != "" {
				t.Fatal("pre-consent error redirected to client")
			}
		})
	}
}

func TestOIDCImmutableSubjectToken(t *testing.T) {
	f := newFixtureIdentity(t, true, true, true, "alice@example.com", fixtureIdentity{subject: "AbC"})
	f.s.mu.Lock()
	f.s.cfg.Users = []config.User{
		{Subject: "email:alice@example.com", Scopes: []string{"ops:read", "ops:write"}},
		{Subject: "oidc:AbC", Scopes: []string{"ops:read"}},
	}
	f.s.mu.Unlock()
	status, tokens := f.token(t, f.tokenForm(f.code(t)))
	if status != 200 {
		t.Fatalf("token: %d %v", status, tokens)
	}
	info, err := f.s.Verify(context.Background(), tokens["access_token"].(string), nil)
	if err != nil || info.UserID != "oidc:AbC" || strings.Join(info.Scopes, " ") != "ops:read" {
		t.Fatalf("immutable subject or scopes lost: %+v %v", info, err)
	}
	f.s.mu.Lock()
	f.s.cfg.Users = f.s.cfg.Users[:1]
	f.s.mu.Unlock()
	f.verify(t, tokens["access_token"].(string), false)
}
