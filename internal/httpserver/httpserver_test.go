package httpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/authn"
	"github.com/gkdis6/opsgate/internal/config"
)

func testOptions(t *testing.T) (config.Config, Options) {
	t.Helper()
	cfg := config.Default()
	cfg.HTTP.PublicURL = "https://mcp.example.com/"
	var tokens []config.StaticToken
	for _, entry := range []struct {
		name, raw string
		scopes    []string
	}{
		{"reader", "read-token", []string{config.ScopeRead}},
		{"writer", "write-token", []string{config.ScopeWrite}},
	} {
		sum := sha256.Sum256([]byte(entry.raw))
		tokens = append(tokens, config.StaticToken{Name: entry.name, SHA256: hex.EncodeToString(sum[:]), Scopes: entry.scopes})
	}
	v, err := authn.Static(tokens)
	if err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "Caller identity"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		user := "missing"
		if req.Extra != nil && req.Extra.TokenInfo != nil {
			user = req.Extra.TokenInfo.UserID
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: user}}}, nil, nil
	})
	return cfg, Options{Server: server, Verifier: v}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}

func TestSDKRoundTrip(t *testing.T) {
	cfg, options := testOptions(t)
	ts := newPipeServer(Handler(cfg, options))
	defer ts.Close()
	client := ts.Client()
	client.Transport = bearerTransport{client.Transport, "read-token"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || text.Text != "static:reader" {
		t.Fatalf("unexpected content: %+v", result.Content)
	}
}

func TestAuthentication(t *testing.T) {
	cfg, options := testOptions(t)
	h := Handler(cfg, options)
	for _, tt := range []struct {
		name, token string
		status      int
	}{
		{"missing", "", 401}, {"invalid", "invalid", 401}, {"no read scope", "write-token", 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := publicRequest(http.MethodPost, "/mcp", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.status {
				t.Fatalf("status=%d", w.Code)
			}
			challenge := w.Header().Get("WWW-Authenticate")
			if !strings.Contains(challenge, `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`) {
				t.Fatalf("challenge=%q", challenge)
			}
			// Clients request exactly the challenge's scopes: a 401 must not
			// narrow them, and insufficient_scope must offer every scope.
			if tt.status == 401 && strings.Contains(challenge, "scope=") {
				t.Fatalf("401 challenge narrows scopes: %q", challenge)
			}
			if tt.status == 403 && !strings.Contains(challenge, `error="insufficient_scope", scope="ops:read ops:write dev:run"`) {
				t.Fatalf("403 challenge=%q", challenge)
			}
		})
	}
	options.Verifier = nil
	w := httptest.NewRecorder()
	r := publicRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer any")
	Handler(cfg, options).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("nil verifier did not fail closed")
	}
}

func TestMetadata(t *testing.T) {
	for _, servers := range [][]string{nil, {"https://issuer.example.com"}} {
		cfg, o := testOptions(t)
		o.AuthorizationServers = servers
		h := Handler(cfg, o)
		for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, publicRequest("GET", path, nil))
			var data map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil {
				t.Fatalf("bad metadata: %s", w.Body.String())
			}
			if data["resource"] != cfg.ResourceURL() || data["resource_name"] != cfg.Server.Name {
				t.Fatalf("metadata=%v", data)
			}
			_, hasServers := data["authorization_servers"]
			if hasServers != (len(servers) > 0) {
				t.Fatal("authorization servers omission")
			}
			if len(servers) > 0 && data["authorization_servers"].([]any)[0] != servers[0] {
				t.Fatal("wrong authorization server")
			}
			if got := data["scopes_supported"].([]any); len(got) != len(config.Scopes) || got[0] != config.ScopeRead || got[1] != config.ScopeWrite || got[2] != config.ScopeDev {
				t.Fatal("wrong scopes")
			}
			if got := data["bearer_methods_supported"].([]any); len(got) != 1 || got[0] != "header" {
				t.Fatal("wrong bearer methods")
			}
		}
	}
}

func TestRoutesAndCORS(t *testing.T) {
	cfg, o := testOptions(t)
	o.Routes = []func(*http.ServeMux){func(mux *http.ServeMux) {
		mux.HandleFunc("/extra", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("extra")) })
	}}
	h := Handler(cfg, o)
	for _, path := range []string{"/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server", "/oauth/token", "/oauth/register", "/oauth/revoke", "/healthz", "/extra", "/oauth/authorize", "/approve/123"} {
		t.Run(path, func(t *testing.T) {
			r := publicRequest("OPTIONS", path, nil)
			r.Header.Set("Origin", "https://inspector.example.com")
			r.Header.Set("Access-Control-Request-Method", "POST")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			allowed := path != "/healthz" && path != "/extra" && path != "/oauth/authorize" && path != "/approve/123"
			if allowed {
				if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
					t.Fatalf("preflight: %d %v", w.Code, w.Header())
				}
				if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") || !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), "Mcp-Session-Id") || !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), "WWW-Authenticate") {
					t.Fatal("missing CORS headers")
				}
			} else if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS outside allowed paths")
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("unexpected common headers")
			}
		})
	}
	for path, want := range map[string]string{"/healthz": "ok", "/extra": "extra"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, publicRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("route %s failed", path)
		}
	}
}

func TestRecoveryAndLogging(t *testing.T) {
	var logs bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prior)
	cfg, o := testOptions(t)
	o.Routes = []func(*http.ServeMux){func(mux *http.ServeMux) {
		mux.HandleFunc("/oauth/token", func(http.ResponseWriter, *http.Request) { panic("panic-secret") })
	}}
	req := publicRequest("POST", "/oauth/token?code=query-secret", strings.NewReader("body-secret"))
	req.Header.Set("Authorization", "Bearer header-secret")
	w := httptest.NewRecorder()
	Handler(cfg, o).ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("status=%d", w.Code)
	}
	all := logs.String() + w.Body.String()
	for _, secret := range []string{"panic-secret", "query-secret", "body-secret", "header-secret"} {
		if strings.Contains(all, secret) {
			t.Fatal("secret leaked")
		}
	}
	if !strings.Contains(logs.String(), "status=500") || !strings.Contains(logs.String(), "path=\"/oauth/token\"") {
		t.Fatalf("missing access log: %s", logs.String())
	}
}

func TestServeListenError(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.Listen = "invalid-address"
	if err := Serve(context.Background(), cfg, http.NotFoundHandler()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestStatusWriterFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: recorder}
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatal(err)
	}
	if !recorder.Flushed || w.status != 200 {
		t.Fatal("flush not forwarded")
	}
	if _, err := io.WriteString(w, "event"); err != nil {
		t.Fatal(err)
	}
	w.WriteHeader(500)
	if recorder.Code != 200 {
		t.Fatal("status changed after flush")
	}
}

// A pipe listener exercises HTTP streaming without opening sandbox TCP sockets.
type pipeListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.connections <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}

type pipeServer struct {
	URL    string
	server *http.Server
	client *http.Client
	done   chan struct{}
}

func newPipeServer(h http.Handler) *pipeServer {
	listener := &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
	s := &pipeServer{URL: "http://mcp.example.com", server: &http.Server{Handler: h}, client: &http.Client{Transport: &http.Transport{DialContext: listener.dial}}, done: make(chan struct{})}
	go func() { defer close(s.done); s.server.Serve(listener) }()
	return s
}
func (s *pipeServer) Client() *http.Client { return s.client }
func (s *pipeServer) Close()               { s.client.CloseIdleConnections(); s.server.Close(); <-s.done }

func TestGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			w.Write([]byte("finished"))
		case <-r.Context().Done():
		}
	})}
	defer srv.Close()
	listener := &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
	shutdown := make(chan error, 1)
	go func() { shutdown <- serve(ctx, srv, listener) }()
	client := &http.Client{Transport: &http.Transport{DialContext: listener.dial}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response := make(chan error, 1)
	go func() {
		res, err := client.Get("http://mcp.example/")
		if err == nil {
			var body []byte
			body, err = io.ReadAll(res.Body)
			res.Body.Close()
			if err == nil && string(body) != "finished" {
				err = fmt.Errorf("unexpected response: %q", body)
			}
		}
		response <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case <-listener.done:
	case <-time.After(3 * time.Second):
		t.Fatal("listener not closed")
	}
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown abandoned active request: %v", err)
	default:
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not complete")
	}
}

func TestAccessLogHidesApprovalIDsAndQueries(t *testing.T) {
	var logs bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prior)
	h := common(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/approve/approval-secret?token=query-secret", "/approve/%61pproval-secret?code=query-secret", "/oauth/callback?code=query-secret", "/mcp?token=query-secret"} {
		h.ServeHTTP(httptest.NewRecorder(), publicRequest("GET", path, nil))
	}
	if strings.Contains(logs.String(), "approval-secret") || strings.Contains(logs.String(), "query-secret") || strings.Contains(logs.String(), "?") {
		t.Fatalf("secret in access log: %s", logs.String())
	}
	if strings.Count(logs.String(), `path="/approve/*"`) != 2 || !strings.Contains(logs.String(), `path="/oauth/callback"`) || !strings.Contains(logs.String(), `path="/mcp"`) {
		t.Fatalf("missing sanitized paths: %s", logs.String())
	}
}

func publicRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "mcp.example.com"
	return r
}

// Sessionless clients (ChatGPT sends server/discover and never falls back)
// must see their version supported; older clients must keep sessions.
func TestProtocolVersions(t *testing.T) {
	cfg, options := testOptions(t)
	h := Handler(cfg, options)
	post := func(version, body string, headers ...string) *httptest.ResponseRecorder {
		r := publicRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		r.Header.Set("Authorization", "Bearer read-token")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if version != "" {
			r.Header.Set("Mcp-Protocol-Version", version)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"openai-mcp","version":"1.0.0"},"io.modelcontextprotocol/clientCapabilities":{}}`
	w := post("2026-07-28", `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+meta+`}}`, "Mcp-Method", "server/discover")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"supportedVersions":["2026-07-28"`) || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("discover: %d %s %v", w.Code, w.Body, w.Header())
	}
	w = post("2026-07-28", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{},`+meta+`}}`, "Mcp-Method", "tools/call", "Mcp-Name", "whoami")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "static:reader") {
		t.Fatalf("sessionless call: %d %s", w.Code, w.Body)
	}
	w = post("", `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`)
	if w.Code != 200 || w.Header().Get("Mcp-Session-Id") == "" || !strings.Contains(w.Body.String(), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("legacy initialize: %d %s %v", w.Code, w.Body, w.Header())
	}

	for _, version := range []string{"", "2025-11-25"} {
		ts := newPipeServer(h)
		client := ts.Client()
		client.Transport = bearerTransport{client.Transport, "read-token"}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: client}, &mcp.ClientSessionOptions{ProtocolVersion: version})
		if err != nil {
			t.Fatalf("%q: %v", version, err)
		}
		want := version
		if want == "" {
			want = "2026-07-28"
		}
		if got := session.InitializeResult().ProtocolVersion; got != want {
			t.Fatalf("negotiated %q, want %q", got, want)
		}
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}}); err != nil {
			t.Fatalf("%q: %v", version, err)
		}
		session.Close()
		cancel()
		ts.Close()
	}
}
