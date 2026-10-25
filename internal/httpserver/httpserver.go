// Package httpserver serves authenticated MCP over Streamable HTTP.
package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
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
		stateful.ServeHTTP(w, r)
	})
}
