package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

const (
	pendingCookie    = "__Host-og_auth"
	sessionCookie    = "__Host-og_session"
	bodyLimit        = 64 << 10
	maxPending       = 1000
	maxClientPending = 20
	// Keep loginBurst + loginTTL/loginInterval <= maxLoginPending.
	maxLoginPending = 256
	loginTTL        = 10 * time.Minute
	loginInterval   = 3 * time.Second
	loginBurst      = 20
	maxTokens       = 100000
)

func random() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func hash(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }
func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(hash(a)), []byte(hash(b))) == 1
}
func loopback(host string) bool { return loopbackOrigin(host) }
func loopbackOrigin(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func secureEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && u.User == nil && !strings.Contains(raw, "#") && (u.Scheme == "https" || u.Scheme == "http" && loopbackOrigin(u.Hostname()))
}
func supportedScopes(scopes []string) bool {
	for _, scope := range scopes {
		if !config.KnownScope(scope) {
			return false
		}
	}
	return true
}
func intersection(a, b []string) []string {
	out := []string{}
	for _, v := range a {
		if slices.Contains(b, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
func subset(a, b []string) bool {
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}
	return true
}
func (s *Server) user(subject string) ([]string, bool) {
	u, ok := s.configuredUser(subject)
	if !ok {
		return nil, false
	}
	return intersection(u.Scopes, config.Scopes), true
}

func (s *Server) configuredUser(subject string) (config.User, bool) {
	for _, u := range s.cfg.Users {
		if subject == u.Subject || !strings.HasPrefix(subject, "oidc:") && !strings.HasPrefix(u.Subject, "oidc:") && strings.EqualFold(subject, u.Subject) {
			return u, true
		}
	}
	return config.User{}, false
}

// VerifyWithTools verifies an access token and attaches the user's tool policy.
func (s *Server) VerifyWithTools(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	info, err := s.Verify(ctx, token, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.configuredUser(info.UserID)
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	info.Extra["tools"] = slices.Clone(u.Tools)
	return info, nil
}

// Access returns what a configured user may do when acting from a browser.
func (s *Server) Access(subject string) (scopes, tools []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.configuredUser(subject)
	if !ok {
		return nil, nil, false
	}
	return intersection(u.Scopes, config.Scopes), slices.Clone(u.Tools), true
}

func pkceSyntax(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}
func localPath(path string) bool {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n\x00") {
		return false
	}
	return true
}
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, status int, code string) {
	jsonReply(w, status, map[string]string{"error": code})
}
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	readDeadline(w)
	noStore(w)
	mediatype, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediatype != "application/x-www-form-urlencoded" {
		oauthError(w, 400, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	if r.ParseForm() != nil || len(r.URL.RawQuery) > 0 {
		oauthError(w, 400, "invalid_request")
		return false
	}
	for _, values := range r.PostForm {
		if len(values) != 1 {
			oauthError(w, 400, "invalid_request")
			return false
		}
	}
	return true
}
func query(r *http.Request) (url.Values, bool) {
	if len(r.URL.RawQuery) > bodyLimit {
		return nil, false
	}
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, false
	}
	for _, values := range v {
		if len(values) != 1 {
			return nil, false
		}
	}
	return v, true
}
func cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (s *Server) redirectResponse(uri, state string, params url.Values) response {
	u, _ := url.Parse(uri)
	v := u.Query()
	for k, values := range params {
		v[k] = values
	}
	v.Set("state", state)
	v.Set("iss", s.issuer)
	u.RawQuery = v.Encode()
	return redirectResponse(u.String())
}

// OAuth libraries read bodies themselves; cap transport reads to bound hostile upstreams.
type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedBody{Reader: io.LimitReader(resp.Body, 1<<20), Closer: resp.Body}
	return resp, nil
}

type boundedBody struct {
	io.Reader
	io.Closer
}

// Bound POST parsing and any body draining performed when writing the response.
func readDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
}

func rejectGETBody(w http.ResponseWriter, r *http.Request) bool {
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.ContentLength > 0 || len(r.TransferEncoding) > 0) {
		// Do not drain an attacker-controlled body before returning the error.
		w.Header().Set("Connection", "close")
		htmlError(w, http.StatusBadRequest, "Request body is not allowed")
		return false
	}
	return true
}
