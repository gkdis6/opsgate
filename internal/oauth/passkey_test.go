package oauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/gkdis6/opsgate/internal/config"
)

const pkIssuer = "https://ops.example"

var b64 = base64.RawURLEncoding

// softAuthenticator is a minimal ES256 platform authenticator for tests.
type softAuthenticator struct {
	key    *ecdsa.PrivateKey
	credID []byte
	handle []byte
	count  uint32
}

func newSoftAuthenticator(t *testing.T) *softAuthenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softAuthenticator{key: key, credID: id}
}

func clientData(t *testing.T, typ, challenge, origin string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (a *softAuthenticator) authData(rpID string, flags byte, attested bool, t *testing.T) []byte {
	rp := sha256.Sum256([]byte(rpID))
	var buf bytes.Buffer
	buf.Write(rp[:])
	buf.WriteByte(flags)
	binary.Write(&buf, binary.BigEndian, a.count)
	if attested {
		buf.Write(make([]byte, 16)) // AAGUID
		binary.Write(&buf, binary.BigEndian, uint16(len(a.credID)))
		buf.Write(a.credID)
		x, y := a.key.PublicKey.X.FillBytes(make([]byte, 32)), a.key.PublicKey.Y.FillBytes(make([]byte, 32))
		cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(cose)
	}
	return buf.Bytes()
}

// create answers registration options the way navigator.credentials.create would.
func (a *softAuthenticator) create(t *testing.T, options map[string]any, origin string) map[string]any {
	pk := options["publicKey"].(map[string]any)
	a.handle, _ = b64.DecodeString(pk["user"].(map[string]any)["id"].(string))
	rpID := pk["rp"].(map[string]any)["id"].(string)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(rpID, 0x45, true, t)})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData(t, "webauthn.create", pk["challenge"].(string), origin)),
			"attestationObject": b64.EncodeToString(att),
		},
	}
}

// get answers login options the way navigator.credentials.get would.
func (a *softAuthenticator) get(t *testing.T, options map[string]any, origin, rpID string) map[string]any {
	pk := options["publicKey"].(map[string]any)
	a.count++
	auth := a.authData(rpID, 0x05, false, t)
	cd := clientData(t, "webauthn.get", pk["challenge"].(string), origin)
	sum := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(auth),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.handle),
		},
	}
}

type pkFixture struct {
	s   *Server
	mux *http.ServeMux
	cfg config.BuiltinAuth
}

func newPasskeyFixture(t *testing.T, stateFile string, users ...string) *pkFixture {
	t.Helper()
	cfg := config.Default().Auth.Builtin
	cfg.StateFile = stateFile
	cfg.Upstream = config.Upstream{Provider: "passkey"}
	for _, u := range users {
		cfg.Users = append(cfg.Users, config.User{Subject: u, Scopes: []string{config.ScopeRead}})
	}
	s, err := New(cfg, pkIssuer, pkIssuer+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	s.Routes(mux)
	return &pkFixture{s: s, mux: mux, cfg: cfg}
}

func (f *pkFixture) do(t *testing.T, method, target string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, pkIssuer+target, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	return m
}

func (f *pkFixture) invite(t *testing.T, subject string) string {
	t.Helper()
	link, err := CreatePasskeyInvite(f.cfg, pkIssuer, subject, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	return u.Query().Get("invite")
}

func (f *pkFixture) enroll(t *testing.T, a *softAuthenticator, subject string) {
	t.Helper()
	token := f.invite(t, subject)
	if w := f.do(t, "GET", "/passkey/register?invite="+token, nil, nil); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || !strings.Contains(w.Body.String(), `src="/passkey.js"`) {
		t.Fatalf("register page %d %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
	opts := decode(t, f.do(t, "POST", "/passkey/register/begin", map[string]string{"invite": token}, nil))
	w := f.do(t, "POST", "/passkey/register/finish", map[string]any{"invite": token, "credential": a.create(t, opts, pkIssuer)}, nil)
	if w.Code != 200 {
		t.Fatalf("finish %d %s", w.Code, w.Body)
	}
	// One-time: the invite is gone.
	if w := f.do(t, "GET", "/passkey/register?invite="+token, nil, nil); w.Code != 404 {
		t.Fatalf("invite reusable: %d", w.Code)
	}
}

// startAuthorize registers a client and begins an authorization, returning
// the pending cookie that the passkey login is bound to.
func (f *pkFixture) startAuthorize(t *testing.T) []*http.Cookie {
	t.Helper()
	reg := decode(t, f.do(t, "POST", "/oauth/register", map[string]any{"client_name": "test", "redirect_uris": []string{"https://client.example/cb"}}, nil))
	q := url.Values{"response_type": {"code"}, "client_id": {reg["client_id"].(string)}, "redirect_uri": {"https://client.example/cb"},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}, "state": {"xyz"}}
	w := f.do(t, "GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/oauth/passkey" {
		t.Fatalf("authorize %d %q", w.Code, w.Header().Get("Location"))
	}
	return w.Result().Cookies()
}

func TestPasskeyRegisterAndLogin(t *testing.T) {
	f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
	a := newSoftAuthenticator(t)
	f.enroll(t, a, "passkey:alice")

	cookies := f.startAuthorize(t)
	if w := f.do(t, "GET", "/oauth/passkey", nil, cookies); w.Code != 200 || !strings.Contains(w.Body.String(), "Sign in with passkey") {
		t.Fatalf("login page %d", w.Code)
	}
	opts := decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
	w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": a.get(t, opts, pkIssuer, "ops.example")}, cookies)
	if w.Code != 200 || decode(t, w)["next"] != "/oauth/passkey/complete" {
		t.Fatalf("finish %d %s", w.Code, w.Body)
	}
	w = f.do(t, "GET", "/oauth/passkey/complete", nil, cookies)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "passkey:alice") || !strings.Contains(w.Body.String(), "client.example") {
		t.Fatalf("consent page %d %s", w.Code, w.Body)
	}
	// Completing twice must not mint a second consent.
	if w := f.do(t, "GET", "/oauth/passkey/complete", nil, cookies); w.Code == 200 {
		t.Fatal("completion replayed")
	}
}

func TestPasskeyLoginRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(f *pkFixture, a *softAuthenticator, opts map[string]any) (map[string]any, string)
	}{
		{"wrong origin", func(f *pkFixture, a *softAuthenticator, o map[string]any) (map[string]any, string) {
			return a.get(nil, o, "https://evil.example", "ops.example"), ""
		}},
		{"wrong rp id", func(f *pkFixture, a *softAuthenticator, o map[string]any) (map[string]any, string) {
			return a.get(nil, o, pkIssuer, "evil.example"), ""
		}},
		{"unknown user handle", func(f *pkFixture, a *softAuthenticator, o map[string]any) (map[string]any, string) {
			a.handle = []byte("someone-else")
			return a.get(nil, o, pkIssuer, "ops.example"), ""
		}},
		{"cloned authenticator", func(f *pkFixture, a *softAuthenticator, o map[string]any) (map[string]any, string) {
			a.count = 10
			cred := a.get(nil, o, pkIssuer, "ops.example") // stored counter becomes 11 after first login
			return cred, "second"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
			a := newSoftAuthenticator(t)
			f.enroll(t, a, "passkey:alice")
			cookies := f.startAuthorize(t)
			opts := decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
			cred, phase := tc.mutate(f, a, opts)
			if phase == "second" {
				// First login succeeds and records counter 11; replaying a
				// lower counter on a new challenge must be refused.
				if w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": cred}, cookies); w.Code != 200 {
					t.Fatalf("first login %d %s", w.Code, w.Body)
				}
				cookies = f.startAuthorize(t)
				opts = decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
				a.count = 3
				cred = a.get(nil, opts, pkIssuer, "ops.example")
			}
			if w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": cred}, cookies); w.Code != http.StatusForbidden {
				t.Fatalf("accepted: %d %s", w.Code, w.Body)
			}
			if w := f.do(t, "GET", "/oauth/passkey/complete", nil, cookies); w.Code == 200 {
				t.Fatal("completed after rejection")
			}
		})
	}
}

func TestPasskeyChallengeIsSingleUse(t *testing.T) {
	f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
	a := newSoftAuthenticator(t)
	f.enroll(t, a, "passkey:alice")
	cookies := f.startAuthorize(t)
	opts := decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
	cred := a.get(t, opts, pkIssuer, "ops.example")
	if w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": cred}, cookies); w.Code != 200 {
		t.Fatalf("first %d", w.Code)
	}
	if w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": cred}, cookies); w.Code == 200 {
		t.Fatal("assertion replayed")
	}
}

func TestPasskeyLoginNeedsPendingCookie(t *testing.T) {
	f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
	if w := f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, nil); w.Code != 400 {
		t.Fatalf("begin without login: %d", w.Code)
	}
	if w := f.do(t, "GET", "/oauth/passkey", nil, nil); w.Code != 400 {
		t.Fatalf("page without login: %d", w.Code)
	}
}

func TestPasskeyUserRemovedFromConfig(t *testing.T) {
	f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
	a := newSoftAuthenticator(t)
	f.enroll(t, a, "passkey:alice")
	cookies := f.startAuthorize(t)
	opts := decode(t, f.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
	if w := f.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": a.get(t, opts, pkIssuer, "ops.example")}, cookies); w.Code != 200 {
		t.Fatalf("finish %d", w.Code)
	}
	f.s.mu.Lock()
	f.s.cfg.Users = nil
	f.s.mu.Unlock()
	if w := f.do(t, "GET", "/oauth/passkey/complete", nil, cookies); w.Code != http.StatusForbidden {
		t.Fatalf("removed user completed: %d", w.Code)
	}
}

func TestPasskeyInvites(t *testing.T) {
	dir := t.TempDir()
	f := newPasskeyFixture(t, filepath.Join(dir, "state.json"), "passkey:alice")
	if _, err := CreatePasskeyInvite(f.cfg, pkIssuer, "passkey:mallory", time.Hour); err == nil {
		t.Fatal("invite for unconfigured subject")
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 43) + "!", "../../etc/passwd"} {
		if w := f.do(t, "GET", "/passkey/register?invite="+url.QueryEscape(bad), nil, nil); w.Code != 404 {
			t.Fatalf("bad invite %q: %d", bad, w.Code)
		}
	}
	token := f.invite(t, "passkey:alice")
	f.s.mu.Lock()
	f.s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	f.s.mu.Unlock()
	if w := f.do(t, "GET", "/passkey/register?invite="+token, nil, nil); w.Code != 404 {
		t.Fatalf("expired invite: %d", w.Code)
	}
	// Invite files are private.
	entries, _ := os.ReadDir(filepath.Join(dir, inviteDirName))
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("invite file mode %v", info.Mode())
		}
	}
}

func TestPasskeyBootstrapLog(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint(persistent), func(t *testing.T) {
			var logs bytes.Buffer
			prior := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(prior)
			state := ""
			if persistent {
				state = filepath.Join(t.TempDir(), "state.json")
			}
			f := newPasskeyFixture(t, state, "passkey:alice")
			const prefix = "passkey setup for passkey:alice (valid 24h): " + pkIssuer + "/passkey/register?invite="
			_, token, ok := strings.Cut(logs.String(), prefix)
			if !ok {
				t.Fatalf("missing invitation in startup log: %q", logs.String())
			}
			token = strings.TrimSpace(token)
			if b, err := b64.DecodeString(token); err != nil || len(b) != 32 {
				t.Fatalf("startup token = %q, err = %v", token, err)
			}
			if w := f.do(t, "POST", "/passkey/register/begin", map[string]string{"invite": token}, nil); w.Code != http.StatusOK {
				t.Fatalf("startup invite unusable: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestPasskeyPersistenceAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	f := newPasskeyFixture(t, state, "passkey:alice", "passkey:bob")
	a := newSoftAuthenticator(t)
	f.enroll(t, a, "passkey:alice")
	f.s.Close()

	g := newPasskeyFixture(t, state, "passkey:alice", "passkey:bob")
	if got := g.s.passkeyMissing(); len(got) != 1 || got[0] != "passkey:bob" {
		t.Fatalf("missing after reload = %v", got)
	}
	// Bootstrap left exactly one live invite, for bob.
	entries, _ := os.ReadDir(filepath.Join(dir, inviteDirName))
	var subjects []string
	for _, e := range entries {
		if inv := readInvite(filepath.Join(dir, inviteDirName), strings.TrimSuffix(e.Name(), ".json")); inv != nil {
			subjects = append(subjects, inv.Subject)
		}
	}
	if len(subjects) != 1 || subjects[0] != "passkey:bob" {
		t.Fatalf("bootstrap invites = %v", subjects)
	}
	// Alice can still sign in with the persisted credential.
	cookies := g.startAuthorize(t)
	opts := decode(t, g.do(t, "POST", "/oauth/passkey/begin", map[string]any{}, cookies))
	if w := g.do(t, "POST", "/oauth/passkey/finish", map[string]any{"credential": a.get(t, opts, pkIssuer, "ops.example")}, cookies); w.Code != 200 {
		t.Fatalf("login after reload %d %s", w.Code, w.Body)
	}
}

func TestPasskeyScriptAndOtherPagesStayScriptFree(t *testing.T) {
	f := newPasskeyFixture(t, "", "passkey:alice")
	w := f.do(t, "GET", "/passkey.js", nil, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("script %d %v", w.Code, w.Header())
	}
	w = f.do(t, "GET", "/oauth/authorize?client_id=nope", nil, nil)
	if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("non-passkey page CSP %q", csp)
	}
}

func TestPasskeyPageSurvivesAnotherBrowser(t *testing.T) {
	f := newPasskeyFixture(t, filepath.Join(t.TempDir(), "state.json"), "passkey:alice")
	const target = "/approve/restart/main/deployment/apps/web"

	// The signed-out link keeps its address in the sign-in address.
	r := httptest.NewRequest("GET", "https://ops.example"+target, nil)
	w := httptest.NewRecorder()
	f.s.RequireUser(func(http.ResponseWriter, *http.Request, string) { t.Fatal("unexpected session") }).ServeHTTP(w, r)
	location := w.Header().Get("Location")
	if w.Code != http.StatusSeeOther || location != "/oauth/passkey?return="+url.QueryEscape(target) {
		t.Fatalf("link: %d %q", w.Code, location)
	}

	// The browser that holds the cookie stays on the sign-in page.
	if page := f.do(t, "GET", location, nil, w.Result().Cookies()); page.Code != http.StatusOK {
		t.Fatalf("with cookie: %d", page.Code)
	}
	// Another browser, with no cookie, goes back to the link and starts again.
	other := f.do(t, "GET", location, nil, nil)
	if other.Code != http.StatusSeeOther || other.Header().Get("Location") != target {
		t.Fatalf("without cookie: %d %q", other.Code, other.Header().Get("Location"))
	}

	// Only approval pages are accepted, so this cannot redirect elsewhere.
	for _, bad := range []string{"", "/admin/clients", "/approve", "//evil.example/approve/x", "https://evil.example/approve/x", "/\\evil.example/approve/x",
		"/approve/../admin/clients", "/approve/%2e%2e/admin/clients", "/approve//evil.example", "/approve/x?next=//evil.example"} {
		w := f.do(t, "GET", "/oauth/passkey?return="+url.QueryEscape(bad), nil, nil)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Fatalf("return %q: %d %q", bad, w.Code, w.Header().Get("Location"))
		}
	}

	// Other sign-ins keep the plain address.
	r = httptest.NewRequest("GET", "https://ops.example/admin/clients", nil)
	w = httptest.NewRecorder()
	f.s.RequireUser(func(http.ResponseWriter, *http.Request, string) { t.Fatal("unexpected session") }).ServeHTTP(w, r)
	if w.Header().Get("Location") != "/oauth/passkey" {
		t.Fatalf("admin login: %q", w.Header().Get("Location"))
	}
	if got := passkeyPagePath(&pendingAuth{Kind: "authorize", ReturnPath: target}); got != "/oauth/passkey" {
		t.Fatalf("authorize path = %q", got)
	}
}
