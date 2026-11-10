// Package httpserver serves authenticated MCP over Streamable HTTP.
package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/time/rate"

	"github.com/gkdis6/opsgate/internal/config"
)

// Options are the parts assembled by main.
type Options struct {
	Server   *mcp.Server
	Verifier auth.TokenVerifier
	// AuthorizationServers are advertised in protected resource metadata.
	AuthorizationServers []string
	// Routes registers extra handlers (authorization server, approvals).
	Routes []func(*http.ServeMux)
}

// Handler returns the root handler for cfg.
func Handler(cfg config.Config, o Options) http.Handler {
	mux := http.NewServeMux()
	verifier := o.Verifier
	if verifier == nil {
		verifier = func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
			return nil, auth.ErrInvalidToken
		}
	}
	getServer := func(*http.Request) *mcp.Server { return o.Server }
	stream := protocolSplit(
		mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute, DisableLocalhostProtection: true}),
		mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true}),
	)
	prm := cfg.Issuer() + "/.well-known/oauth-protected-resource/mcp"
	// The SDK puts its required scopes into the 401 challenge, and MCP clients
	// request exactly the challenge's scopes, so requiring ops:read there made
	// clients ask for read-only tokens. Leave the challenge without a scope
	// (clients then use scopes_supported) and enforce ops:read here instead.
	mux.Handle("/mcp", auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: prm,
	})(requireScope(config.ScopeRead, prm, stream)))
	metadata := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: cfg.ResourceURL(), AuthorizationServers: o.AuthorizationServers,
		ScopesSupported:        config.Scopes,
		BearerMethodsSupported: []string{"header"}, ResourceName: cfg.Server.Name,
	})
	mux.Handle("/.well-known/oauth-protected-resource", metadata)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", metadata)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	for _, routes := range o.Routes {
		routes(mux)
	}
	return common(checkHost(cfg, cors(mux)))
}

// requireScope rejects tokens lacking scope with RFC 6750 insufficient_scope,
// advertising every scope so that a client re-authorizing gets a usable token.
func requireScope(scope, resourceMetadata string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ti := auth.TokenInfoFromContext(r.Context()); ti == nil || !slices.Contains(ti.Scopes, scope) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="insufficient_scope", scope=%q, resource_metadata=%q`,
				strings.Join(config.Scopes, " "), resourceMetadata))
			http.Error(w, "insufficient scope", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sessionlessProtocol is the first MCP version without sessions (SEP-2575).
const sessionlessProtocol = "2026-07-28"

// protocolSplit serves sessionless-protocol requests statelessly and the rest
// with sessions. The SDK offers the new protocol only on stateless handlers,
// while older clients need sessions for elicitation; and some new clients
// (ChatGPT) do not fall back to initialize when discovery omits their version.
// Versions are ISO dates, so string order is release order.
func protocolSplit(stateful, stateless http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Protocol-Version") >= sessionlessProtocol {
			stateless.ServeHTTP(w, r)
			return
		}
		// Each request without a session ID starts a session the SDK keeps
		// until its idle timeout. Admit them at a bounded rate per user so one
		// token cannot open sessions without limit.
		if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" && !sessions.allow(r) {
			w.Header().Set("Retry-After", "3")
			http.Error(w, "too many new MCP sessions; retry shortly", http.StatusTooManyRequests)
			return
		}
		stateful.ServeHTTP(w, r)
	})
}

// sessionAdmission allows a burst of 20 new sessions per user and one more
// every 3 seconds: at most about 620 live sessions per user within the
// 30-minute idle timeout.
type sessionAdmission struct {
	mu    sync.Mutex
	users map[string]*rate.Limiter
}

var sessions = &sessionAdmission{users: map[string]*rate.Limiter{}}

func (s *sessionAdmission) allow(r *http.Request) bool {
	user := ""
	if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
		user = ti.UserID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	limiter := s.users[user]
	if limiter == nil {
		// Users come from verified tokens; cap the map anyway.
		if len(s.users) >= 1024 {
			clear(s.users)
		}
		limiter = rate.NewLimiter(rate.Every(3*time.Second), 20)
		s.users[user] = limiter
	}
	return limiter.Allow()
}
