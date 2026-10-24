package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"

	"github.com/gkdis6/opsgate/internal/config"
)

// External verifies JWT access tokens against the issuer's discovered JWKS.
func External(ctx context.Context, cfg config.ExternalAuth, resource string) (auth.TokenVerifier, error) {
	audience := cfg.Audience
	if audience == "" {
		audience = resource
	}
	if audience == "" || !validEndpoint(cfg.Issuer) {
		return nil, fmt.Errorf("external auth requires an issuer URL and audience")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if supplied, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && supplied != nil {
		*client = *supplied
		client.Timeout = 10 * time.Second
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = boundedTransport{transport}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	ctx = oidc.ClientContext(ctx, client)
	metadata, err := discover(ctx, client, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	verifier := oidc.NewVerifier(cfg.Issuer, oidc.NewRemoteKeySet(ctx, metadata.JWKS), &oidc.Config{
		ClientID:             audience,
		SupportedSigningAlgs: []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.PS256, oidc.EdDSA},
	})
	claim := cfg.UserClaim
	if claim == "" {
		claim = "sub"
	}
	users := make(map[string][]string, len(cfg.Users))
	for _, user := range cfg.Users {
		if _, exists := users[user.Subject]; exists {
			return nil, fmt.Errorf("external auth users must be unique")
		}
		users[user.Subject] = slices.Clone(user.Scopes)
	}
	return func(ctx context.Context, raw string, _ *http.Request) (*auth.TokenInfo, error) {
		if len(raw) == 0 || len(raw) > maxTokenBytes {
			return nil, auth.ErrInvalidToken
		}
		token, err := verifier.Verify(ctx, raw)
		if err != nil || token.Issuer != cfg.Issuer || !token.Expiry.After(time.Now()) {
			return nil, auth.ErrInvalidToken
		}
		var claims map[string]json.RawMessage
		if token.Claims(&claims) != nil {
			return nil, auth.ErrInvalidToken
		}
		var user string
		if json.Unmarshal(claims[claim], &user) != nil || user == "" {
			return nil, auth.ErrInvalidToken
		}
		scopes, ok := tokenScopes(claims)
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		if len(users) > 0 {
			allowed, found := users["claim:"+user]
			if !found {
				return nil, auth.ErrInvalidToken
			}
			scopes = slices.DeleteFunc(scopes, func(s string) bool { return !slices.Contains(allowed, s) })
		}
		return &auth.TokenInfo{UserID: user, Scopes: scopes, Expiration: token.Expiry}, nil
	}, nil
}

func tokenScopes(claims map[string]json.RawMessage) ([]string, bool) {
	raw, exists := claims["scope"]
	if !exists {
		raw, exists = claims["scp"]
	}
	if !exists {
		return nil, true
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return nil, false
	}
	var scopes []string
	switch value := decoded.(type) {
	case string:
		scopes = strings.Fields(value)
	case []any:
		if _, hasScope := claims["scope"]; hasScope {
			return nil, false
		}
		for _, item := range value {
			scope, ok := item.(string)
			if !ok {
				return nil, false
			}
			scopes = append(scopes, scope)
		}
	default:
		return nil, false
	}
	var out []string
	for _, s := range scopes {
		if (s == config.ScopeRead || s == config.ScopeWrite) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, true
}
