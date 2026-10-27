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
func (s *Server) authenticate(r *http.Request) (*client, response) {
	id := r.PostForm.Get("client_id")
	secret := r.PostForm.Get("client_secret")
	method := "none"
	if len(r.Header.Values("Authorization")) > 1 {
		return nil, errorResponse(401, "invalid_client")
	}
	if header := r.Header.Get("Authorization"); header != "" {
		basicID, basicSecret, ok := r.BasicAuth()
		if !ok || r.PostForm.Has("client_secret") {
			return nil, errorResponse(401, "invalid_client")
		}
		var err error
		basicID, err = url.QueryUnescape(basicID)
		if err != nil {
			return nil, errorResponse(401, "invalid_client")
		}
		basicSecret, err = url.QueryUnescape(basicSecret)
		if err != nil {
			return nil, errorResponse(401, "invalid_client")
		}
		if id != "" && id != basicID {
			return nil, errorResponse(401, "invalid_client")
		}
		id, secret, method = basicID, basicSecret, "client_secret_basic"
	} else if r.PostForm.Has("client_secret") {
		method = "client_secret_post"
	}
	c := s.clients[id]
	if c == nil || c.Pending || c.Method != method || (method != "none" && subtle.ConstantTimeCompare([]byte(hash(secret)), []byte(c.SecretHash)) != 1) {
		return nil, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Basic realm="opsgate"`)
			errorResponse(401, "invalid_client")(w, r)
		}
	}
	return c, nil
}
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.tokenResponse(r)(w, r)
}
func (s *Server) tokenResponse(r *http.Request) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil || s.closed {
		return errorResponse(503, "server_error")
	}
	c, failure := s.authenticate(r)
	if failure != nil {
		return failure
	}
	grant := r.PostForm.Get("grant_type")
	if grant == "" {
		return errorResponse(400, "invalid_request")
	}
	if grant != "authorization_code" && grant != "refresh_token" {
		return errorResponse(400, "unsupported_grant_type")
	}
	if !slices.Contains(c.GrantTypes, grant) {
		return errorResponse(400, "unauthorized_client")
	}
	s.sweep()
	if grant == "authorization_code" {
		if r.PostForm.Get("code") == "" {
			return errorResponse(400, "invalid_request")
		}
		return s.redeemCode(r, c)
	} else {
		if r.PostForm.Get("refresh_token") == "" {
			return errorResponse(400, "invalid_request")
		}
		return s.rotate(r, c)
	}
}
func (s *Server) redeemCode(r *http.Request, c *client) response {
	code := s.codes[hash(r.PostForm.Get("code"))]
	if code == nil || code.ClientID != c.ID {
		return errorResponse(400, "invalid_grant")
	}
	// Replays by the authenticated client invalidate all descendants, even if
	// their original code expiry has passed. RetainUntil follows the family.
	if code.Used {
		if s.revokeFamily(code.Family) && s.save() != nil {
			return errorResponse(503, "server_error")
		} else {
			return errorResponse(400, "invalid_grant")
		}
	}
	if r.PostForm.Has("resource") && r.PostForm.Get("resource") != s.resource {
		return errorResponse(400, "invalid_target")
	}
	verifier := r.PostForm.Get("code_verifier")
	if !s.now().Before(code.Expires) || code.RedirectURI != r.PostForm.Get("redirect_uri") || code.Resource != s.resource || !pkceSyntax(verifier) || !equal(oauth2.S256ChallengeFromVerifier(verifier), code.Challenge) {
		return errorResponse(400, "invalid_grant")
	}
	scopes, ok := s.user(code.Subject)
	if !ok {
		return errorResponse(400, "invalid_grant")
	}
	if s.revoked(code.Family) {
		return errorResponse(400, "invalid_grant")
	}
	if len(s.access)+len(s.refresh)+2 > maxTokens {
		return errorResponse(http.StatusServiceUnavailable, "temporarily_unavailable")
	}
	code.Used = true
	return s.issue(c, code.Subject, intersection(code.Scopes, scopes), code.Family)
}
func (s *Server) rotate(r *http.Request, c *client) response {
	refresh := s.refresh[hash(r.PostForm.Get("refresh_token"))]
	if refresh == nil || refresh.ClientID != c.ID || s.revoked(refresh.Family) {
		return errorResponse(400, "invalid_grant")
	}
	if refresh.Used {
		if s.revokeFamily(refresh.Family) && s.save() != nil {
			return errorResponse(503, "server_error")
		} else {
			return errorResponse(400, "invalid_grant")
		}
	}
	if r.PostForm.Has("resource") && r.PostForm.Get("resource") != s.resource {
		return errorResponse(400, "invalid_target")
	}
	if !s.now().Before(refresh.Expires) {
		return errorResponse(400, "invalid_grant")
	}
	current, ok := s.user(refresh.Subject)
	if !ok {
		return errorResponse(400, "invalid_grant")
	}
	scopes := intersection(refresh.Scopes, current)
	if r.PostForm.Has("scope") {
		requested := strings.Fields(r.PostForm.Get("scope"))
		if !subset(requested, scopes) {
			return errorResponse(400, "invalid_scope")
		}
		scopes = intersection(requested, scopes)
	}
	if len(s.access)+len(s.refresh)+2 > maxTokens {
		return errorResponse(http.StatusServiceUnavailable, "temporarily_unavailable")
	}
	refresh.Used = true
	return s.issue(c, refresh.Subject, scopes, refresh.Family)
}
func (s *Server) issue(c *client, subject string, scopes []string, family string) response {
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
		return errorResponse(503, "server_error")
	}
	out := map[string]any{"access_token": accessRaw, "token_type": "Bearer", "expires_in": int64(s.cfg.AccessTokenTTL / time.Second), "scope": strings.Join(scopes, " ")}
	if slices.Contains(c.GrantTypes, "refresh_token") {
		out["refresh_token"] = refreshRaw
	}
	return jsonResponse(200, out)
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	s.revokeResponse(r)(w, r)
}
func (s *Server) revokeResponse(r *http.Request) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageErr != nil || s.closed {
		return errorResponse(503, "server_error")
	}
	c, failure := s.authenticate(r)
	if failure != nil {
		return failure
	}
	if r.PostForm.Get("token") == "" {
		return errorResponse(400, "invalid_request")
	}
	key := hash(r.PostForm.Get("token"))
	changed := false
	if t := s.refresh[key]; t != nil && t.ClientID == c.ID {
		changed = s.revokeFamily(t.Family)
	}
	if t := s.access[key]; t != nil && t.ClientID == c.ID {
		delete(s.access, key)
		changed = true
	}
	if changed && s.save() != nil {
		return errorResponse(503, "server_error")
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		noStore(w)
		w.WriteHeader(http.StatusOK)
	}
}
