// Package oauth implements opsgate's built-in OAuth authorization server.
package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/time/rate"

	"github.com/gkdis6/opsgate/internal/config"
)

// Server is the authorization server. It is safe for concurrent use.
type Server struct {
	mu                sync.Mutex
	cfg               config.BuiltinAuth
	issuer, resource  string
	clients           map[string]*client
	access            map[string]*accessToken
	refresh           map[string]*refreshToken
	families          map[string]time.Time
	codes             map[string]*authorizationCode
	pending           map[string]*pendingAuth
	sessions          map[string]*browserSession
	passkey           *webauthn.WebAuthn // non-nil when the upstream is "passkey"
	passkeys          map[string]*passkeyUser
	registrations     map[string]*passkeyRegistration
	invites           map[string]*passkeyInvite
	upstream          oauth2.Config
	verifier          *oidc.IDTokenVerifier
	httpClient        *http.Client
	githubUserURL     string
	registrationLimit *rate.Limiter
	loginLimit        *rate.Limiter
	now               func() time.Time
	storageErr        error
	stop              chan struct{}
	done              chan struct{}
	closeErr          error
	closed            bool
	closeOnce         sync.Once
}

// New returns a Server whose issuer is issuer (an https origin) and whose
// tokens are only valid for resource (the MCP endpoint URL).
func New(cfg config.BuiltinAuth, issuer, resource string) (*Server, error) {
	return newServer(cfg, issuer, resource, nil)
}

func newServer(cfg config.BuiltinAuth, issuer, resource string, transport http.RoundTripper) (*Server, error) {
	validation := config.Default()
	validation.Auth.Builtin = cfg
	if err := validation.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(issuer, "#") || (u.Scheme != "https" && !(u.Scheme == "http" && loopbackOrigin(u.Hostname()))) {
		return nil, fmt.Errorf("issuer must be an https origin")
	}
	if resource != issuer+"/mcp" {
		return nil, fmt.Errorf("resource must be the issuer's /mcp endpoint")
	}
	if len(cfg.Users) == 0 {
		return nil, fmt.Errorf("at least one user is required")
	}
	for _, user := range cfg.Users {
		if user.Subject == "" || !supportedScopes(user.Scopes) {
			return nil, fmt.Errorf("invalid user configuration")
		}
	}
	var secret string
	if cfg.Upstream.Provider == "passkey" {
		// No external identity provider: nothing to read.
	} else if cfg.Upstream.ClientSecretFile != "" {
		b, e := os.ReadFile(cfg.Upstream.ClientSecretFile)
		if e != nil {
			return nil, fmt.Errorf("cannot read upstream secret file")
		}
		secret = strings.TrimSpace(string(b))
	} else if cfg.Upstream.ClientSecretEnv != "" {
		secret = strings.TrimSpace(os.Getenv(cfg.Upstream.ClientSecretEnv))
	}
	if cfg.Upstream.Provider != "passkey" && (secret == "" || cfg.Upstream.ClientID == "") {
		return nil, fmt.Errorf("upstream client ID and secret are required")
	}
	if cfg.AccessTokenTTL <= 0 || cfg.RefreshTokenTTL <= 0 || cfg.MaxClients <= 0 {
		return nil, fmt.Errorf("token TTLs and max clients must be positive")
	}
	s := &Server{cfg: cfg, issuer: issuer, resource: resource, clients: map[string]*client{}, access: map[string]*accessToken{}, refresh: map[string]*refreshToken{}, families: map[string]time.Time{}, codes: map[string]*authorizationCode{}, pending: map[string]*pendingAuth{}, sessions: map[string]*browserSession{}, passkeys: map[string]*passkeyUser{}, registrations: map[string]*passkeyRegistration{}, invites: map[string]*passkeyInvite{}, registrationLimit: rate.NewLimiter(rate.Every(3*time.Second), 5), now: time.Now, githubUserURL: "https://api.github.com/user", stop: make(chan struct{}), done: make(chan struct{})}
	s.loginLimit = rate.NewLimiter(rate.Every(loginInterval), loginBurst)
	if transport == nil {
		transport = http.DefaultTransport
	}
	s.httpClient = &http.Client{Timeout: 15 * time.Second, Transport: boundedTransport{base: transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.upstream = oauth2.Config{ClientID: cfg.Upstream.ClientID, ClientSecret: secret, RedirectURL: issuer + "/oauth/callback"}
	switch cfg.Upstream.Provider {
	case "passkey":
		rp, e := newPasskeyRP(issuer)
		if e != nil {
			return nil, fmt.Errorf("passkey relying party: %w", e)
		}
		s.passkey = rp
	case "github":
		s.upstream.Endpoint = github.Endpoint
		s.upstream.Scopes = []string{"read:user"}
	case "oidc":
		upstreamIssuer, parseErr := url.Parse(cfg.Upstream.Issuer)
		if parseErr != nil || !secureEndpoint(cfg.Upstream.Issuer) || upstreamIssuer.RawQuery != "" || upstreamIssuer.ForceQuery {
			return nil, fmt.Errorf("invalid upstream OIDC issuer")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		provider, e := oidc.NewProvider(oidc.ClientContext(ctx, s.httpClient), cfg.Upstream.Issuer)
		if e != nil {
			return nil, fmt.Errorf("upstream OIDC discovery failed")
		}
		s.upstream.Endpoint = provider.Endpoint()
		if !secureEndpoint(s.upstream.Endpoint.AuthURL) || !secureEndpoint(s.upstream.Endpoint.TokenURL) {
			return nil, fmt.Errorf("insecure upstream OIDC endpoint")
		}
		s.upstream.Scopes = []string{oidc.ScopeOpenID, "email"}
		s.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.Upstream.ClientID})
	default:
		return nil, fmt.Errorf("unsupported upstream provider")
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	if s.passkey != nil {
		s.bootstrapInvites()
	}
	go s.sweepLoop()
	return s, nil
}

// Routes registers OAuth discovery, /oauth/* and the /admin/clients page.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.metadata)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("GET /oauth/callback", s.callback)
	mux.HandleFunc("POST /oauth/consent", s.consent)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
	s.adminRoutes(mux)
	if s.passkey != nil {
		s.passkeyRoutes(mux)
	}
}

// Verify implements auth.TokenVerifier for access tokens issued by s.
func (s *Server) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	if len(token) > 256 || !strings.HasPrefix(token, "og_at_") {
		return nil, auth.ErrInvalidToken
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.access[hash(token)]
	if s.storageErr != nil || s.closed || t == nil || s.clients[t.ClientID] == nil || s.clients[t.ClientID].Pending || !s.now().Before(t.Expires) || s.revoked(t.Family) {
		return nil, auth.ErrInvalidToken
	}
	scopes, ok := s.user(t.Subject)
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	if c := s.clients[t.ClientID]; c != nil {
		c.LastUsed = s.now()
	}
	return &auth.TokenInfo{Scopes: intersection(t.Scopes, scopes), Expiration: t.Expires, UserID: t.Subject, Extra: map[string]any{"client_id": t.ClientID}}, nil
}

// RequireUser runs next with an allowed upstream subject, starting a browser
// login when no live session is present. Login returns only to local paths.
func (s *Server) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rejectGETBody(w, r) {
			return
		}
		path := r.URL.RequestURI()
		if !localPath(path) {
			htmlError(w, 400, "Invalid return path")
			return
		}
		s.mu.Lock()
		if s.storageErr != nil || s.closed {
			s.mu.Unlock()
			htmlError(w, 503, "Authorization unavailable")
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil {
			if session := s.sessions[hash(c.Value)]; session != nil && s.now().Before(session.Expires) {
				if _, ok := s.user(session.Subject); ok {
					subject := session.Subject
					s.mu.Unlock()
					next(w, r, subject)
					return
				}
			}
		}
		reply := s.startLogin(&pendingAuth{Kind: "login", ReturnPath: path})
		s.mu.Unlock()
		reply(w, r)
	})
}

// Close stops background sweeping and flushes state to disk.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closeErr = s.save()
		s.closed = true
	})
	return s.closeErr
}
