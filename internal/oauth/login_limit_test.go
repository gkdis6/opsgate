package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/approval"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestLoginFloodPreservesTransaction(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	f.s.cfg.Admins = []string{"github:alice"}
	now := time.Now()
	f.s.now = func() time.Time { return now }
	first := f.request(t, "GET", "/admin/clients", "", false)
	if first.StatusCode != http.StatusSeeOther {
		t.Fatalf("start login: %d", first.StatusCode)
	}
	upstream := *f.browser
	upstream.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	callback, err := upstream.Get(first.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback.Body.Close()
	if callback.StatusCode != http.StatusFound {
		t.Fatalf("upstream login: %d", callback.StatusCode)
	}
	for range maxLoginPending + 1 {
		w := httptest.NewRecorder()
		f.s.RequireUser(f.s.clientsPage).ServeHTTP(w, httptest.NewRequest("GET", "/admin/clients", nil))
	}
	// Keep admitting requests throughout the original transaction's lifetime.
	for elapsed := loginInterval; elapsed < loginTTL; elapsed += loginInterval {
		f.s.mu.Lock()
		now = now.Add(loginInterval)
		f.s.mu.Unlock()
		w := httptest.NewRecorder()
		f.s.RequireUser(f.s.clientsPage).ServeHTTP(w, httptest.NewRequest("GET", "/admin/clients", nil))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("admission after refill: %d", w.Code)
		}
	}
	finished := f.request(t, "GET", strings.TrimPrefix(callback.Header.Get("Location"), f.web.URL), "", true)
	body, _ := io.ReadAll(finished.Body)
	if finished.StatusCode != http.StatusOK || !strings.Contains(string(body), "Manage clients") {
		t.Fatalf("login after flood: status=%d, invalid state=%t", finished.StatusCode, strings.Contains(string(body), "Invalid login state"))
	}
}

func TestLoginAdmissionPaths(t *testing.T) {
	for _, provider := range []string{"github", "passkey"} {
		for _, route := range []string{"admin", "approval"} {
			t.Run(provider+"/"+route, func(t *testing.T) {
				var s *Server
				var mux *http.ServeMux
				if provider == "github" {
					f := newFixture(t, false, true, true, "Alice")
					s = f.s
					mux = f.web.transport.(*testTransport).handlers[f.web.URL].(*http.ServeMux)
				} else {
					f := newPasskeyFixture(t, "", "passkey:alice")
					s, mux = f.s, f.mux
				}
				path := "/admin/clients"
				if route == "approval" {
					m := approval.New(config.Approval{TTL: time.Minute, Web: true}, s.issuer, s, nil)
					m.Routes(mux)
					_, err := m.Confirm(context.Background(), nil, tool.ConfirmRequest{
						Caller: tool.Caller{User: s.cfg.Users[0].Subject, Transport: "http"},
						Tool:   "test_write", Summary: "Change test value", Args: map[string]any{},
					})
					if !errors.Is(err, tool.ErrApprovalPending) {
						t.Fatalf("create approval: %v", err)
					}
					path = regexp.MustCompile(`/approve/[a-z2-7]{26}`).FindString(err.Error())
					if path == "" {
						t.Fatal("missing approval URL")
					}
				}
				now := time.Now()
				s.now = func() time.Time { return now }
				for range s.loginLimit.Burst() {
					w := httptest.NewRecorder()
					mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
					if w.Code != http.StatusSeeOther {
						t.Fatalf("login admission: %d", w.Code)
					}
				}
				before := len(s.pending)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "3" || !strings.Contains(w.Body.String(), "Try again") {
					t.Fatalf("limited login: status=%d retry=%q", w.Code, w.Header().Get("Retry-After"))
				}
				if len(s.pending) != before || len(s.sessions) != 0 || len(w.Result().Cookies()) != 0 || w.Header().Get("Location") != "" {
					t.Fatal("limited login created state or redirected")
				}
				if w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
					t.Fatal("missing error page protections")
				}
				for _, p := range s.pending {
					if p.Kind != "login" || p.ReturnPath != path || !p.Expires.Equal(now.Add(loginTTL)) {
						t.Fatal("incorrect login binding or TTL")
					}
				}
				s.mu.Lock()
				now = now.Add(loginInterval)
				s.mu.Unlock()
				w = httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != http.StatusSeeOther || len(s.pending) != before+1 {
					t.Fatalf("limiter did not recover: %d", w.Code)
				}
			})
		}
	}
}

func TestLoginAdmissionInvariant(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	limit := f.s.loginLimit
	maximum := float64(limit.Burst()) + float64(limit.Limit())*loginTTL.Seconds()
	if maximum > maxLoginPending {
		t.Fatalf("burst + rate * TTL = %g exceeds login capacity %d", maximum, maxLoginPending)
	}
	t.Logf("maximum admissions per TTL: %g; %d pendingAuth structs: %d bytes (excluding referenced data and map)", maximum, maxLoginPending, uintptr(maxLoginPending)*reflect.TypeOf(pendingAuth{}).Size())
}

func TestLoginLimitDoesNotAffectAuthorization(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		f := newFixture(t, false, true, true, "Alice")
		if !f.s.loginLimit.AllowN(f.s.now(), f.s.loginLimit.Burst()) {
			t.Fatal("cannot exhaust login limiter")
		}
		r := f.request(t, "GET", "/oauth/authorize?"+f.authQuery().Encode(), "", false)
		if r.StatusCode != http.StatusSeeOther {
			t.Fatalf("authorization limited: %d", r.StatusCode)
		}
	})
	t.Run("passkey", func(t *testing.T) {
		f := newPasskeyFixture(t, "", "passkey:alice")
		if !f.s.loginLimit.AllowN(f.s.now(), f.s.loginLimit.Burst()) {
			t.Fatal("cannot exhaust login limiter")
		}
		f.startAuthorize(t)
	})
}

func TestLoginExpiredEntriesFreed(t *testing.T) {
	f := newFixture(t, false, true, true, "Alice")
	now := time.Now()
	f.s.now = func() time.Time { return now }
	for i := range maxLoginPending {
		f.s.pending[strings.Repeat("x", i+1)] = &pendingAuth{Kind: "login", Expires: now}
	}
	live := &pendingAuth{Kind: "login", Expires: now.Add(loginTTL)}
	f.s.pending["live"] = live
	w := httptest.NewRecorder()
	f.s.RequireUser(f.s.clientsPage).ServeHTTP(w, httptest.NewRequest("GET", "/admin/clients", nil))
	if w.Code != http.StatusSeeOther || len(f.s.pending) != 2 || f.s.pending["live"] != live {
		t.Fatalf("expired entries not freed safely: status=%d pending=%d", w.Code, len(f.s.pending))
	}
}

func TestPasskeyLoginFloodPreservesTransaction(t *testing.T) {
	f := newPasskeyFixture(t, t.TempDir()+"/state.json", "passkey:alice")
	f.s.cfg.Admins = []string{"passkey:alice"}
	a := newSoftAuthenticator(t)
	f.enroll(t, a, "passkey:alice")
	first := f.do(t, "GET", "/admin/clients", nil, nil)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("start login: %d", first.Code)
	}
	cookies := first.Result().Cookies()
	opts := decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
	for range maxLoginPending + 1 {
		f.do(t, "GET", "/admin/clients", nil, nil)
	}
	w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": a.get(t, opts, pkIssuer, "ops.example")}, cookies)
	if w.Code != http.StatusOK {
		t.Fatalf("passkey finish after flood: %d", w.Code)
	}
	w = f.do(t, "GET", "/oauth/passkey/complete", nil, cookies)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/clients" {
		t.Fatalf("passkey complete after flood: %d", w.Code)
	}
	w = f.do(t, "GET", "/admin/clients", nil, w.Result().Cookies())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Manage clients") {
		t.Fatalf("admin session after flood: %d", w.Code)
	}
}
