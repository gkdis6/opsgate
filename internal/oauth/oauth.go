// Package oauth is opsgate's built-in OAuth 2.1 authorization server.
//
// Scope is deliberately small: authorization code with PKCE (S256 only),
// refresh token rotation, Dynamic Client Registration and a per-client
// consent screen. People authenticate at an upstream identity provider
// (GitHub or any OIDC provider); opsgate issues its own opaque tokens and
// stores only their hashes.
package oauth

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/gkdis6/opsgate/internal/config"
)

// Server is the authorization server. It is safe for concurrent use.
type Server struct {
	// TODO(codex): implement.
}

// New returns a Server whose issuer is issuer (an https origin) and whose
// tokens are only valid for resource (the MCP endpoint URL).
func New(cfg config.BuiltinAuth, issuer, resource string) (*Server, error) {
	// TODO(codex): implement.
	return &Server{}, nil
}

// Routes registers /.well-known/oauth-authorization-server and /oauth/*.
func (s *Server) Routes(mux *http.ServeMux) {
	// TODO(codex): implement.
}

// Verify implements auth.TokenVerifier for access tokens issued by s.
func (s *Server) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	// TODO(codex): implement.
	return nil, auth.ErrInvalidToken
}

// RequireUser implements approval.BrowserAuth: it runs next with the
// signed-in upstream subject (e.g. "github:octocat"), first sending the
// browser through upstream login if needed. Only configured users pass.
func (s *Server) RequireUser(next func(w http.ResponseWriter, r *http.Request, subject string)) http.Handler {
	// TODO(codex): implement.
	return http.NotFoundHandler()
}

// Close flushes state to disk.
func (s *Server) Close() error {
	// TODO(codex): implement.
	return nil
}
