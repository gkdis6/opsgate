package oauth

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/gkdis6/opsgate/internal/ui"
)

// Passkeys let a self-hosted server sign people in without an external
// identity provider. Registration needs a one-time invite created by the
// operator (`opsgate passkey invite`, or logged at first start); login
// replaces the upstream redirect and then continues exactly like an upstream
// callback, so user allowlists, consent and browser sessions are unchanged.

//go:embed passkey.js
var passkeyScript []byte

// Passkey pages are the only ones that run script: the same-origin
// /passkey.js, nothing inline, no other origins.
const passkeyCSP = "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'unsafe-inline'; " + ui.ImageSource + "; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

const passkeyCeremonyTTL = 5 * time.Minute

// passkeyUser is persisted in the state file. Handle is the random WebAuthn
// user handle; authorization still keys on the configured subject.
type passkeyUser struct {
	Handle      []byte                `json:"handle"`
	Credentials []webauthn.Credential `json:"credentials"`
}

type webauthnUser struct {
	subject string
	user    *passkeyUser
}

func (u webauthnUser) WebAuthnID() []byte   { return u.user.Handle }
func (u webauthnUser) WebAuthnName() string { return u.subject }
func (u webauthnUser) WebAuthnDisplayName() string {
	_, name, _ := strings.Cut(u.subject, ":")
	if name == "" {
		return u.subject
	}
	return name
}
func (u webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.user.Credentials }

// passkeyRegistration is an in-progress passkey creation, keyed by invite hash.
type passkeyRegistration struct {
	subject string
	handle  []byte
	session *webauthn.SessionData
	expires time.Time
}

func newPasskeyRP(issuer string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "opsgate",
		RPOrigins:     []string{issuer},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
}

func (s *Server) passkeyRoutes(mux *http.ServeMux) {
	protect := http.NewCrossOriginProtection()
	mux.HandleFunc("GET /passkey.js", servePasskeyScript)
	mux.HandleFunc("GET /passkey/register", s.passkeyRegisterPage)
	mux.Handle("POST /passkey/register/begin", protect.Handler(http.HandlerFunc(s.passkeyRegisterBegin)))
	mux.Handle("POST /passkey/register/finish", protect.Handler(http.HandlerFunc(s.passkeyRegisterFinish)))
	mux.HandleFunc("GET /oauth/passkey", s.passkeyLoginPage)
	mux.Handle("POST /oauth/passkey/begin", protect.Handler(http.HandlerFunc(s.passkeyLoginBegin)))
	mux.Handle("POST /oauth/passkey/finish", protect.Handler(http.HandlerFunc(s.passkeyLoginFinish)))
	mux.HandleFunc("GET /oauth/passkey/complete", s.passkeyLoginComplete)
}

func servePasskeyScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(passkeyScript)
}

var passkeyPage = ui.NewTemplate(`{{define "title"}}{{.Title}}{{end}}
{{define "content"}}<h1>{{.Title}}</h1><p>{{.Lead}}</p>
{{if .Subject}}<dl><dt>Account</dt><dd>{{.Subject}}</dd></dl>{{end}}
<div id="passkey" data-mode="{{.Mode}}" data-invite="{{.Invite}}"><div class="actions"><button type="button" id="passkey-start" class="primary">{{.Button}}</button></div>
<p id="passkey-status" class="muted" role="status" hidden></p></div>{{end}}
{{define "scripts"}}<script src="/passkey.js" defer></script>{{end}}`)

type passkeyPageData struct {
	Title, Lead, Subject, Mode, Invite, Button string
}

func passkeyPageResponse(data passkeyPageData) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		noStore(w)
		w.Header().Set("Content-Security-Policy", passkeyCSP)
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ui.Render(w, http.StatusOK, passkeyPage, data)
	}
}

func (s *Server) passkeyRegisterPage(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	token := r.URL.Query().Get("invite")
	s.mu.Lock()
	inv, _ := s.lookupInvite(token)
	s.mu.Unlock()
	if inv == nil {
		htmlError(w, http.StatusNotFound, "This invite link is invalid or has expired. Ask the server operator for a new one.")
		return
	}
	passkeyPageResponse(passkeyPageData{
		Title:   "Create your passkey",
		Lead:    "This creates a passkey for this opsgate server on your device (Face ID, Touch ID, Windows Hello, a phone or a security key). Your fingerprint or face never leaves the device.",
		Subject: inv.Subject, Mode: "register", Invite: token, Button: "Create passkey",
	})(w, r)
}

type inviteRequest struct {
	Invite     string          `json:"invite"`
	Credential json.RawMessage `json:"credential"`
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	readDeadline(w)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		errorResponse(http.StatusUnsupportedMediaType, "unsupported_media_type")(w, r)
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(v); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		errorResponse(http.StatusBadRequest, "invalid_request")(w, r)
		return false
	}
	return true
}

func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var req inviteRequest
	if !readJSON(w, r, &req) {
		return
	}
	s.passkeyRegisterBeginResponse(req.Invite)(w, r)
}

func (s *Server) passkeyRegisterBeginResponse(token string) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, key := s.lookupInvite(token)
	if inv == nil {
		return errorResponse(http.StatusNotFound, "invalid or expired invite")
	}
	user := s.passkeys[inv.Subject]
	if user == nil {
		user = &passkeyUser{Handle: randomBytes(32)}
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(user.Credentials))
	for _, c := range user.Credentials {
		exclusions = append(exclusions, c.Descriptor())
	}
	creation, session, err := s.passkey.BeginRegistration(webauthnUser{inv.Subject, user}, webauthn.WithExclusions(exclusions))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "cannot start registration")
	}
	s.registrations[key] = &passkeyRegistration{subject: inv.Subject, handle: user.Handle, session: session, expires: s.now().Add(passkeyCeremonyTTL)}
	// CredentialCreation marshals as {"publicKey": {...}}, the shape the
	// browser API takes.
	return jsonResponse(http.StatusOK, creation)
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	var req inviteRequest
	if !readJSON(w, r, &req) {
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		errorResponse(http.StatusBadRequest, "invalid credential")(w, r)
		return
	}
	s.passkeyRegisterFinishResponse(req.Invite, parsed)(w, r)
}

func (s *Server) passkeyRegisterFinishResponse(token string, parsed *protocol.ParsedCredentialCreationData) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, key := s.lookupInvite(token)
	reg := s.registrations[key]
	if inv == nil || reg == nil || reg.subject != inv.Subject || !s.now().Before(reg.expires) {
		return errorResponse(http.StatusNotFound, "invalid or expired invite")
	}
	delete(s.registrations, key)
	user := s.passkeys[reg.subject]
	if user == nil {
		user = &passkeyUser{Handle: reg.handle}
	}
	cred, err := s.passkey.CreateCredential(webauthnUser{reg.subject, user}, *reg.session, parsed)
	if err != nil {
		return errorResponse(http.StatusBadRequest, "passkey verification failed")
	}
	user.Credentials = append(user.Credentials, *cred)
	s.passkeys[reg.subject] = user
	if err := s.consumeInvite(key); err != nil {
		return errorResponse(http.StatusServiceUnavailable, "cannot consume invite")
	}
	if s.save() != nil {
		return errorResponse(http.StatusServiceUnavailable, "cannot store passkey")
	}
	return jsonResponse(http.StatusOK, map[string]bool{"ok": true})
}

// pendingFromCookie returns the pending authorization bound to this browser.
// Caller holds mu.
func (s *Server) pendingFromCookie(r *http.Request) (string, *pendingAuth) {
	c, err := r.Cookie(pendingCookie)
	if err != nil {
		return "", nil
	}
	key := hash(c.Value)
	p := s.pending[key]
	if s.storageErr != nil || s.closed || p == nil || !s.now().Before(p.Expires) || p.Subject != "" {
		return "", nil
	}
	return key, p
}

// passkeyPagePath keeps an approval page's address in the sign-in address, so
// another browser can start again from it. OAuth logins never expose their state.
func passkeyPagePath(p *pendingAuth) string {
	if p.Kind == "login" && approvalReturn(p.ReturnPath) {
		return "/oauth/passkey?return=" + url.QueryEscape(p.ReturnPath)
	}
	return "/oauth/passkey"
}

// approvalReturn reports whether target is a plain approval page on this server,
// the only place a sign-in address may send a browser without its cookie.
func approvalReturn(target string) bool {
	if !localPath(target) {
		return false
	}
	u, err := url.ParseRequestURI(target)
	return err == nil && strings.HasPrefix(u.Path, "/approve/") && u.RawQuery == "" && path.Clean(u.Path) == u.Path
}

func (s *Server) passkeyLoginPage(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	s.mu.Lock()
	_, p := s.pendingFromCookie(r)
	s.mu.Unlock()
	if p == nil {
		// "Open in browser" from an app carries this address but not the cookie.
		if target := r.URL.Query().Get("return"); approvalReturn(target) {
			redirectResponse(target)(w, r)
			return
		}
		htmlError(w, http.StatusBadRequest, "This sign-in has expired. Start the connection again from your app.")
		return
	}
	passkeyPageResponse(passkeyPageData{
		Title:  "Sign in to opsgate",
		Lead:   "Use the passkey you created for this server.",
		Mode:   "login",
		Button: "Sign in with passkey",
	})(w, r)
}

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !readJSON(w, r, &req) {
		return
	}
	s.passkeyLoginBeginResponse(r)(w, r)
}

func (s *Server) passkeyLoginBeginResponse(r *http.Request) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, p := s.pendingFromCookie(r)
	if p == nil {
		return errorResponse(http.StatusBadRequest, "sign-in expired")
	}
	assertion, session, err := s.passkey.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "cannot start sign-in")
	}
	p.WebAuthn = session
	return jsonResponse(http.StatusOK, assertion)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credential json.RawMessage `json:"credential"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		errorResponse(http.StatusBadRequest, "invalid credential")(w, r)
		return
	}
	s.passkeyLoginFinishResponse(r, parsed)(w, r)
}

var errUnknownPasskey = errors.New("unknown passkey")

func (s *Server) passkeyLoginFinishResponse(r *http.Request, parsed *protocol.ParsedCredentialAssertionData) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, p := s.pendingFromCookie(r)
	if p == nil || p.WebAuthn == nil {
		return errorResponse(http.StatusBadRequest, "sign-in expired")
	}
	session := *p.WebAuthn
	p.WebAuthn = nil // one assertion per challenge
	var subject string
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		for sub, u := range s.passkeys {
			if equalBytes(u.Handle, userHandle) {
				subject = sub
				return webauthnUser{sub, u}, nil
			}
		}
		return nil, errUnknownPasskey
	}
	_, cred, err := s.passkey.ValidatePasskeyLogin(handler, session, parsed)
	if err != nil || subject == "" {
		return errorResponse(http.StatusForbidden, "passkey not recognised")
	}
	// A counter that did not advance can mean a cloned authenticator.
	if cred.Authenticator.CloneWarning {
		return errorResponse(http.StatusForbidden, "passkey rejected: possible cloned authenticator")
	}
	user := s.passkeys[subject]
	for i := range user.Credentials {
		if equalBytes(user.Credentials[i].ID, cred.ID) {
			user.Credentials[i].Authenticator = cred.Authenticator
			user.Credentials[i].Flags = cred.Flags
		}
	}
	if s.save() != nil {
		return errorResponse(http.StatusServiceUnavailable, "cannot store sign-in")
	}
	p.PasskeySubject = subject
	return jsonResponse(http.StatusOK, map[string]string{"next": "/oauth/passkey/complete"})
}

func (s *Server) passkeyLoginComplete(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	s.mu.Lock()
	key, p := s.pendingFromCookie(r)
	var subject string
	if p != nil && !p.InFlight {
		subject, p.PasskeySubject = p.PasskeySubject, ""
		p.InFlight = subject != ""
	}
	s.mu.Unlock()
	if subject == "" {
		htmlError(w, http.StatusBadRequest, "This sign-in has expired. Start the connection again from your app.")
		return
	}
	s.callbackResponse(key, p, subject, nil)(w, r)
}

func equalBytes(a, b []byte) bool { return len(a) > 0 && string(a) == string(b) }

// passkeyMissing lists configured users without a registered passkey.
func (s *Server) passkeyMissing() []string {
	var missing []string
	for _, u := range s.cfg.Users {
		if p := s.passkeys[u.Subject]; p == nil || len(p.Credentials) == 0 {
			missing = append(missing, u.Subject)
		}
	}
	return missing
}
