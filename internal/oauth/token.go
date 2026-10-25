package oauth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var errIdentity = errors.New("identity verification failed")

// Caller holds mu. Credentials must use precisely the client's registered method.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) *client {
	id := r.PostForm.Get("client_id")
	secret := r.PostForm.Get("client_secret")
	method := "none"
	if len(r.Header.Values("Authorization")) > 1 {
		oauthError(w, 401, "invalid_client")
		return nil
	}
	if header := r.Header.Get("Authorization"); header != "" {
		basicID, basicSecret, ok := r.BasicAuth()
		if !ok || r.PostForm.Has("client_secret") {
			oauthError(w, 401, "invalid_client")
			return nil
		}
		var err error
		basicID, err = url.QueryUnescape(basicID)
		if err != nil {
			oauthError(w, 401, "invalid_client")
			return nil
		}
		basicSecret, err = url.QueryUnescape(basicSecret)
		if err != nil {
			oauthError(w, 401, "invalid_client")
			return nil
		}
		if id != "" && id != basicID {
			oauthError(w, 401, "invalid_client")
			return nil
		}
		id, secret, method = basicID, basicSecret, "client_secret_basic"
	} else if r.PostForm.Has("client_secret") {
		method = "client_secret_post"
	}
	c := s.clients[id]
	if c == nil || c.Method != method || (method != "none" && subtle.ConstantTimeCompare([]byte(hash(secret)), []byte(c.SecretHash)) != 1) {
		w.Header().Set("WWW-Authenticate", `Basic realm="opsgate"`)
		oauthError(w, 401, "invalid_client")
		return nil
	}
	return c
}
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil || s.closed {
		oauthError(w, 503, "server_error")
		return
	}
	c := s.authenticate(w, r)
	if c == nil {
		return
	}
	grant := r.PostForm.Get("grant_type")
	if grant == "" {
		oauthError(w, 400, "invalid_request")
		return
	}
	if grant != "authorization_code" && grant != "refresh_token" {
		oauthError(w, 400, "unsupported_grant_type")
		return
	}
	if !slices.Contains(c.GrantTypes, grant) {
		oauthError(w, 400, "unauthorized_client")
		return
	}
	s.sweep()
	if grant == "authorization_code" {
		if r.PostForm.Get("code") == "" {
			oauthError(w, 400, "invalid_request")
			return
		}
		s.redeemCode(w, r, c)
	} else {
		if r.PostForm.Get("refresh_token") == "" {
			oauthError(w, 400, "invalid_request")
			return
		}
		s.rotate(w, r, c)
	}
}
func (s *Server) redeemCode(w http.ResponseWriter, r *http.Request, c *client) {
	code := s.codes[hash(r.PostForm.Get("code"))]
	if code == nil || code.ClientID != c.ID {
		oauthError(w, 400, "invalid_grant")
		return
	}
	// Replays by the authenticated client invalidate all descendants, even if
	// their original code expiry has passed. RetainUntil follows the family.
	if code.Used {
		s.revokeFamily(code.Family)
		if s.save() != nil {
			oauthError(w, 503, "server_error")
		} else {
			oauthError(w, 400, "invalid_grant")
		}
		return
	}
	if r.PostForm.Has("resource") && r.PostForm.Get("resource") != s.resource {
		oauthError(w, 400, "invalid_target")
		return
	}
	verifier := r.PostForm.Get("code_verifier")
	if !s.now().Before(code.Expires) || code.RedirectURI != r.PostForm.Get("redirect_uri") || code.Resource != s.resource || !pkceSyntax(verifier) || !equal(oauth2.S256ChallengeFromVerifier(verifier), code.Challenge) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	scopes, ok := s.user(code.Subject)
	if !ok {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if s.revoked(code.Family) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if !s.tokenCapacity(w) {
		return
	}
	code.Used = true
	s.issue(w, c, code.Subject, intersection(code.Scopes, scopes), code.Family)
}
func (s *Server) rotate(w http.ResponseWriter, r *http.Request, c *client) {
	refresh := s.refresh[hash(r.PostForm.Get("refresh_token"))]
	if refresh == nil || refresh.ClientID != c.ID || s.revoked(refresh.Family) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if refresh.Used {
		s.revokeFamily(refresh.Family)
		if s.save() != nil {
			oauthError(w, 503, "server_error")
		} else {
			oauthError(w, 400, "invalid_grant")
		}
		return
	}
	if r.PostForm.Has("resource") && r.PostForm.Get("resource") != s.resource {
		oauthError(w, 400, "invalid_target")
		return
	}
	if !s.now().Before(refresh.Expires) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	current, ok := s.user(refresh.Subject)
	if !ok {
		oauthError(w, 400, "invalid_grant")
		return
	}
	scopes := intersection(refresh.Scopes, current)
	if r.PostForm.Has("scope") {
		requested := strings.Fields(r.PostForm.Get("scope"))
		if !subset(requested, scopes) {
			oauthError(w, 400, "invalid_scope")
			return
		}
		scopes = intersection(requested, scopes)
	}
	if !s.tokenCapacity(w) {
		return
	}
	refresh.Used = true
	s.issue(w, c, refresh.Subject, scopes, refresh.Family)
}
func (s *Server) issue(w http.ResponseWriter, c *client, subject string, scopes []string, family string) {
	now := s.now()
	accessRaw, refreshRaw := "og_at_"+random(), "og_rt_"+random()
	access := &accessToken{ClientID: c.ID, Subject: subject, Scopes: scopes, Family: family, Expires: now.Add(s.cfg.AccessTokenTTL)}
	expiry := now.Add(s.cfg.RefreshTokenTTL)
	retain := expiry
	if access.Expires.After(retain) {
		retain = access.Expires
	}
	s.access[hash(accessRaw)] = access
	if slices.Contains(c.GrantTypes, "refresh_token") {
		s.refresh[hash(refreshRaw)] = &refreshToken{accessToken: accessToken{ClientID: c.ID, Subject: subject, Scopes: scopes, Family: family, Expires: expiry}, RetainUntil: retain}
	}
	for _, r := range s.refresh {
		if r.Family == family && r.RetainUntil.Before(retain) {
			r.RetainUntil = retain
		}
	}
	for _, code := range s.codes {
		if code.Family == family && code.RetainUntil.Before(retain) {
			code.RetainUntil = retain
		}
	}
	c.LastUsed = now
	if s.save() != nil {
		oauthError(w, 503, "server_error")
		return
	}
	out := map[string]any{"access_token": accessRaw, "token_type": "Bearer", "expires_in": int64(s.cfg.AccessTokenTTL / time.Second), "scope": strings.Join(scopes, " ")}
	if slices.Contains(c.GrantTypes, "refresh_token") {
		out["refresh_token"] = refreshRaw
	}
	jsonReply(w, 200, out)
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil || s.closed {
		oauthError(w, 503, "server_error")
		return
	}
	c := s.authenticate(w, r)
	if c == nil {
		return
	}
	if r.PostForm.Get("token") == "" {
		oauthError(w, 400, "invalid_request")
		return
	}
	key := hash(r.PostForm.Get("token"))
	if t := s.refresh[key]; t != nil && t.ClientID == c.ID {
		s.revokeFamily(t.Family)
	}
	if t := s.access[key]; t != nil && t.ClientID == c.ID {
		delete(s.access, key)
	}
	if s.save() != nil {
		oauthError(w, 503, "server_error")
		return
	}
	noStore(w)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) tokenCapacity(w http.ResponseWriter) bool {
	if len(s.access)+len(s.refresh)+2 > maxTokens {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	return true
}
