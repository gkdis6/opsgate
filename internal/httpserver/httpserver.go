// Package httpserver serves authenticated MCP over Streamable HTTP.
package httpserver

import (
	"context"
	"net/http"
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
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return o.Server }, &mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute})
	mux.Handle("/mcp", auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.Issuer() + "/.well-known/oauth-protected-resource/mcp",
		Scopes:              []string{config.ScopeRead},
	})(stream))
	metadata := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: cfg.ResourceURL(), AuthorizationServers: o.AuthorizationServers,
		ScopesSupported:        []string{config.ScopeRead, config.ScopeWrite},
		BearerMethodsSupported: []string{"header"}, ResourceName: cfg.Server.Name,
	})
	mux.Handle("/.well-known/oauth-protected-resource", metadata)
	mux.Handle("/.well-known/oauth-protected-resource/mcp", metadata)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	for _, routes := range o.Routes {
		routes(mux)
	}
	return common(cors(mux))
}
