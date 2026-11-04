package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/gkdis6/opsgate/internal/ui"
)

// DCR client names are self-asserted. Keep the redirect host prominent so the
// person can verify the destination independently of that name.
var page = ui.NewTemplate(`{{define "title"}}{{if .Error}}Authorization failed{{else}}Authorize a connection{{end}}{{end}}
{{define "content"}}{{if .Error}}<h1>Authorization failed</h1><p>{{.Error}}</p>{{else}}
<h1>Authorize an unverified client</h1>
<p class="note">Approve only if you started this connection and the redirect host below is the app you are connecting.</p>
<div class="target"><p class="target-label">Redirect host</p><div class="host">{{.Host}}</div></div>
<dl><dt>Client name (unverified, self-reported)</dt><dd>{{.Name}}</dd>
<dt>Redirect URI</dt><dd>{{.URI}}</dd>
<dt>Signed in as</dt><dd>{{.Subject}}</dd></dl>
<h2>Access to grant</h2><ul class="scopes">{{range .Scopes}}<li><span class="chip">{{.}}</span>
{{if eq . "ops:read"}}Read server, Kubernetes and notes{{else if eq . "ops:write"}}Change them after approval{{else if eq . "dev:run"}}Start coding agents after approval{{end}}</li>{{end}}</ul>
<form method="post" action="/oauth/consent"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><div class="actions">
<button class="secondary" name="decision" value="deny">Deny</button><button class="primary" name="decision" value="approve">Approve</button>
</div></form>{{end}}{{end}}`)

type pageData struct {
	Error, Name, Host, URI, Subject, CSRF string
	Scopes                                []string
}

func htmlPage(w http.ResponseWriter, status int, data pageData) {
	htmlHeaders(w)
	ui.Render(w, status, page, data)
}
func htmlHeaders(w http.ResponseWriter) {
	noStore(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; "+ui.ImageSource+"; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func htmlError(w http.ResponseWriter, status int, message string) {
	htmlPage(w, status, pageData{Error: message})
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	v, ok := query(r)
	if !ok {
		htmlError(w, 400, "Invalid authorization request")
		return
	}
	s.authorizeResponse(v)(w, r)
}
func (s *Server) authorizeResponse(v url.Values) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	c := s.clients[v.Get("client_id")]
	if c == nil || !slices.Contains(c.RedirectURIs, v.Get("redirect_uri")) {
		return htmlErrorResponse(400, "Unknown client or redirect URI")
	}
	fail := func(code string) response {
		return htmlErrorResponse(400, code)
	}
	if s.storageErr != nil || s.closed {
		return fail("server_error")
	}
	if c.Pending {
		return pendingClientResponse()
	}
	if v.Get("response_type") == "" {
		return fail("invalid_request")
	}
	if v.Get("response_type") != "code" {
		return fail("unsupported_response_type")
	}
	if !slices.Contains(c.GrantTypes, "authorization_code") {
		return fail("unauthorized_client")
	}
	if v.Get("code_challenge_method") != "S256" || !pkceSyntax(v.Get("code_challenge")) {
		return fail("invalid_request")
	}
	scopes := strings.Fields(v.Get("scope"))
	if !supportedScopes(scopes) {
		return fail("invalid_scope")
	}
	if target := v.Get("resource"); v.Has("resource") && target != s.resource {
		return fail("invalid_target")
	}
	return s.startLogin(&pendingAuth{Kind: "authorize", ClientID: c.ID, RedirectURI: v.Get("redirect_uri"), Challenge: v.Get("code_challenge"), State: v.Get("state"), Requested: scopes})
}

// Caller holds mu; the pending cookie binds the browser to this one transaction.
func (s *Server) startLogin(p *pendingAuth) response {
	s.sweep()
	now := s.now()
	if p.Kind == "login" && !s.loginLimit.AllowN(now, 1) || !s.makePendingRoom(p) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "3")
			htmlError(w, http.StatusTooManyRequests, "Too many sign-in requests. Try again shortly.")
		}
	}
	id := random()
	p.UpstreamState = random()
	p.Verifier = oauth2.GenerateVerifier()
	p.Nonce = random()
	p.CSRF = random()
	p.Expires = now.Add(loginTTL)
	s.pending[hash(id)] = p
	if s.passkey != nil {
		return redirectResponse(passkeyPagePath(p)).withCookie(pendingCookie, id, 600)
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(p.Verifier)}
	if s.verifier != nil {
		opts = append(opts, oidc.Nonce(p.Nonce))
	}
	return redirectResponse(s.upstream.AuthCodeURL(p.UpstreamState, opts...)).withCookie(pendingCookie, id, 600)
}
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	v, ok := query(r)
	if !ok {
		htmlError(w, 400, "Invalid callback")
		return
	}
	c, err := r.Cookie(pendingCookie)
	if err != nil {
		htmlError(w, 400, "Missing login cookie")
		return
	}
	key := hash(c.Value)
	s.mu.Lock()
	p := s.pending[key]
	if s.storageErr != nil || s.closed || p == nil || !s.now().Before(p.Expires) || p.InFlight || p.Subject != "" || !equal(v.Get("state"), p.UpstreamState) || v.Get("code") == "" {
		s.mu.Unlock()
		htmlError(w, 400, "Invalid login state")
		return
	}
	p.InFlight = true
	// Snapshot all request bindings before releasing the lock for upstream I/O.
	verifier, nonce := p.Verifier, p.Nonce
	s.mu.Unlock()
	subject, err := s.resolveIdentity(r.Context(), v.Get("code"), verifier, nonce)
	s.callbackResponse(key, p, subject, err)(w, r)
}
func (s *Server) callbackResponse(key string, p *pendingAuth, subject string, err error) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[key] != p || !s.now().Before(p.Expires) {
		return htmlErrorResponse(400, "Login expired")
	}
	if err != nil {
		delete(s.pending, key)
		return htmlErrorResponse(403, "Upstream identity verification failed").withCookie(pendingCookie, "", -1)
	}
	scopes, allowed := s.user(subject)
	if !allowed || s.storageErr != nil || s.closed {
		delete(s.pending, key)
		return htmlErrorResponse(403, "User is not allowed").withCookie(pendingCookie, "", -1)
	}
	if p.Kind == "login" {
		if !localPath(p.ReturnPath) || len(s.sessions) >= maxPending {
			delete(s.pending, key)
			return htmlErrorResponse(400, "Cannot create browser session")
		}
		session := random()
		s.sessions[hash(session)] = &browserSession{Subject: subject, Expires: s.now().Add(12 * time.Hour)}
		delete(s.pending, key)
		return redirectResponse(p.ReturnPath).withCookie(sessionCookie, session, 12*60*60).withCookie(pendingCookie, "", -1)
	}
	client := s.clients[p.ClientID]
	if client == nil || client.Pending {
		delete(s.pending, key)
		return htmlErrorResponse(400, "Client no longer registered")
	}
	p.Subject = subject
	p.Scopes = scopes
	if len(p.Requested) > 0 {
		p.Scopes = intersection(p.Requested, scopes)
	}
	u, _ := url.Parse(p.RedirectURI)
	host := u.Host
	if host == "" {
		host = u.Scheme + ":"
	}
	return htmlResponse(200, pageData{Name: client.Name, Host: host, URI: p.RedirectURI, Subject: subject, Scopes: p.Scopes, CSRF: p.CSRF})
}
func (s *Server) resolveIdentity(ctx context.Context, code, verifier, nonce string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ctx = oidc.ClientContext(ctx, s.httpClient)
	token, err := s.upstream.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", err
	}
	if s.verifier != nil {
		raw, _ := token.Extra("id_token").(string)
		id, err := s.verifier.Verify(ctx, raw)
		if err != nil {
			return "", err
		}
		if !equal(id.Nonce, nonce) {
			return "", errIdentity
		}
		// Immutable subjects take precedence when explicitly allowlisted. They do
		// not depend on email availability, verification or mailbox ownership.
		if id.Subject != "" {
			subject := "oidc:" + id.Subject
			s.mu.Lock()
			_, allowed := s.user(subject)
			s.mu.Unlock()
			if allowed {
				return subject, nil
			}
		}
		var claims struct {
			HostedDomain string `json:"hd"`
			Email        string `json:"email"`
			Verified     bool   `json:"email_verified"`
		}
		if id.Claims(&claims) != nil || !claims.Verified || len(claims.Email) > 320 || !strings.Contains(claims.Email, "@") || strings.TrimSpace(claims.Email) != claims.Email {
			return "", errIdentity
		}
		email := strings.ToLower(claims.Email)
		local, domain, ok := strings.Cut(email, "@")
		if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
			return "", errIdentity
		}
		if id.Issuer == "https://accounts.google.com" && domain != "gmail.com" && domain != "googlemail.com" && !strings.EqualFold(claims.HostedDomain, domain) {
			return "", errIdentity
		}
		return "email:" + email, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.githubUserURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errIdentity
	}
	var user struct {
		Login string `json:"login"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, bodyLimit))
	if dec.Decode(&user) != nil || user.Login == "" || len(user.Login) > 100 {
		return "", errIdentity
	}
	for _, ch := range user.Login {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
			return "", errIdentity
		}
	}
	return "github:" + strings.ToLower(user.Login), nil
}
func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	cookieValue, err := r.Cookie(pendingCookie)
	if err != nil {
		htmlError(w, 403, "Missing consent cookie")
		return
	}
	s.consentResponse(r, hash(cookieValue.Value))(w, r)
}
func (s *Server) consentResponse(r *http.Request, key string) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending[key]
	if s.storageErr != nil || s.closed || p == nil || p.Kind != "authorize" || p.Subject == "" || !s.now().Before(p.Expires) || r.PostForm.Get("csrf_token") == "" || !equal(r.PostForm.Get("csrf_token"), p.CSRF) {
		return htmlErrorResponse(403, "Invalid consent request")
	}
	decision := r.PostForm.Get("decision")
	if decision != "approve" && decision != "deny" {
		return htmlErrorResponse(400, "Invalid decision")
	}
	delete(s.pending, key)
	if decision == "deny" {
		return s.redirectResponse(p.RedirectURI, p.State, url.Values{"error": {"access_denied"}}).withCookie(pendingCookie, "", -1)
	}
	scopes, ok := s.user(p.Subject)
	if !ok {
		return htmlErrorResponse(403, "User is not allowed").withCookie(pendingCookie, "", -1)
	}
	if s.clients[p.ClientID] == nil || s.clients[p.ClientID].Pending || len(s.codes) >= maxTokens {
		return htmlErrorResponse(503, "Authorization unavailable").withCookie(pendingCookie, "", -1)
	}
	raw := random()
	s.codes[hash(raw)] = &authorizationCode{ClientID: p.ClientID, RedirectURI: p.RedirectURI, Challenge: p.Challenge, Subject: p.Subject, Resource: s.resource, Family: random(), Scopes: intersection(p.Scopes, scopes), Expires: s.now().Add(time.Minute), RetainUntil: s.now().Add(s.cfg.RefreshTokenTTL + s.cfg.AccessTokenTTL + time.Minute)}
	if s.save() != nil {
		return htmlErrorResponse(503, "Authorization unavailable").withCookie(pendingCookie, "", -1)
	}
	return s.redirectResponse(p.RedirectURI, p.State, url.Values{"code": {raw}}).withCookie(pendingCookie, "", -1)
}
