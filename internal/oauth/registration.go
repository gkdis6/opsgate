package oauth

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/config"
)

type registration struct {
	RedirectURIs  []string `json:"redirect_uris"`
	Name          string   `json:"client_name"`
	Method        string   `json:"token_endpoint_auth_method"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	if !rejectGETBody(w, r) {
		return
	}
	jsonReply(w, 200, map[string]any{
		"issuer": s.issuer, "authorization_endpoint": s.issuer + "/oauth/authorize", "token_endpoint": s.issuer + "/oauth/token", "registration_endpoint": s.issuer + "/oauth/register", "revocation_endpoint": s.issuer + "/oauth/revoke",
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"}, "scopes_supported": config.Scopes, "authorization_response_iss_parameter_supported": true,
	})
}
func validRedirect(raw string) bool {
	if len(raw) == 0 || len(raw) > 512 || strings.ContainsAny(raw, "#\\\r\n\t") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Hostname() != "" && u.Opaque == ""
	case "http":
		return loopback(u.Hostname()) && u.Opaque == ""
	case "javascript", "data", "file", "vbscript":
		return false
	default:
		return (strings.Contains(u.Scheme, ".") || u.Scheme == "cursor") && (u.Path != "" || u.Host != "" || u.Opaque != "")
	}
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	readDeadline(w)
	if !s.registrationLimit.Allow() {
		w.Header().Set("Retry-After", "3")
		oauthError(w, 429, "slow_down")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	var in registration
	if dec.Decode(&raw) != nil || json.Unmarshal(raw, &in) != nil {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	for _, key := range []string{"redirect_uris", "client_name", "token_endpoint_auth_method", "grant_types", "response_types"} {
		if value, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			oauthError(w, 400, "invalid_client_metadata")
			return
		}
	}
	if len(in.RedirectURIs) < 1 || len(in.RedirectURIs) > 5 {
		oauthError(w, 400, "invalid_redirect_uri")
		return
	}
	for _, uri := range in.RedirectURIs {
		if !validRedirect(uri) {
			oauthError(w, 400, "invalid_redirect_uri")
			return
		}
	}
	if !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 100 {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	if in.Method == "" {
		in.Method = "none"
	}
	if in.Method != "none" && in.Method != "client_secret_basic" && in.Method != "client_secret_post" {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	if in.GrantTypes == nil {
		in.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if !subset(in.GrantTypes, []string{"authorization_code", "refresh_token"}) || !subset(in.ResponseTypes, []string{"code"}) {
		oauthError(w, 400, "invalid_client_metadata")
		return
	}
	s.registerResponse(in)(w, r)
}
func (s *Server) registerResponse(in registration) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if s.storageErr != nil || s.closed {
		return errorResponse(503, "server_error")
	}
	pending := !s.redirectsAllowed(in.RedirectURIs)
	if pending && s.cfg.UnknownRedirectHosts != "approve" {
		return errorResponse(400, "invalid_redirect_uri")
	}
	s.pruneClients()
	if len(s.clients) >= s.cfg.MaxClients {
		return errorResponse(400, "invalid_client_metadata")
	}
	id := random()
	c := &client{Pending: pending, ID: id, RedirectURIs: in.RedirectURIs, Name: in.Name, Method: in.Method, GrantTypes: in.GrantTypes, Created: s.now()}
	secret := ""
	if in.Method != "none" {
		secret = random()
		c.SecretHash = hash(secret)
	}
	s.clients[id] = c
	if s.save() != nil {
		return errorResponse(503, "server_error")
	}
	out := map[string]any{"client_id": id, "client_id_issued_at": c.Created.Unix(), "redirect_uris": c.RedirectURIs, "client_name": c.Name, "token_endpoint_auth_method": c.Method, "grant_types": c.GrantTypes, "response_types": []string{"code"}}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	reply := jsonResponse(http.StatusCreated, out)
	if pending && s.cfg.NotifyURL != "" {
		notice := clientNotice{Event: "client_pending", ClientID: id, ClientName: c.Name, RedirectHosts: redirectHosts(c.RedirectURIs), ApproveURL: s.issuer + "/admin/clients"}
		return func(w http.ResponseWriter, r *http.Request) {
			s.notifyClient(r.Context(), notice)
			reply(w, r)
		}
	}
	return reply
}
