package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHostPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, listen, host, local string
		allowed                   bool
	}{
		{name: "public proxy host", listen: "127.0.0.1:8787", host: "mcp.example.com", allowed: true},
		{name: "public case insensitive", listen: "0.0.0.0:8787", host: "MCP.EXAMPLE.COM", allowed: true},
		{name: "public wrong port", listen: "0.0.0.0:8787", host: "mcp.example.com:443"},
		{name: "foreign host", listen: "127.0.0.1:8787", host: "evil.example"},
		{name: "public suffix", listen: "127.0.0.1:8787", host: "mcp.example.com.evil"},
		{name: "localhost", listen: "127.0.0.1:8787", host: "localhost:9876", allowed: true},
		{name: "localhost uppercase", listen: "127.0.0.1:8787", host: "LOCALHOST", allowed: true},
		{name: "IPv4 loopback range", listen: "127.0.0.1:8787", host: "127.56.78.90:1234", allowed: true},
		{name: "IPv6 loopback", listen: "[::1]:8787", host: "[::1]:1234", allowed: true},
		{name: "IPv6 loopback no port", listen: "[::1]:8787", host: "[::1]", allowed: true},
		{name: "loopback forbidden on wildcard", listen: "0.0.0.0:8787", host: "localhost"},
		{name: "loopback forbidden on IPv6 wildcard", listen: "[::]:8787", host: "[::1]"},
		{name: "loopback forbidden on external listener", listen: "192.0.2.1:8787", host: "127.0.0.1"},
		{name: "connection cannot widen wildcard", listen: "0.0.0.0:8787", local: "127.0.0.1:8787", host: "localhost"},
		{name: "actual connection rejects config mismatch", listen: "127.0.0.1:8787", local: "192.0.2.1:8787", host: "localhost"},
		{name: "localhost suffix", listen: "127.0.0.1:8787", host: "localhost.evil"},
		{name: "empty host", listen: "127.0.0.1:8787", host: ""},
		{name: "userinfo", listen: "127.0.0.1:8787", host: "evil@localhost"},
		{name: "path", listen: "127.0.0.1:8787", host: "localhost/path"},
		{name: "bad port", listen: "127.0.0.1:8787", host: "localhost:abc"},
		{name: "port overflow", listen: "127.0.0.1:8787", host: "localhost:65536"},
		{name: "empty port", listen: "127.0.0.1:8787", host: "localhost:"},
		{name: "unbracketed IPv6", listen: "127.0.0.1:8787", host: "::1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, o := testOptions(t)
			cfg.HTTP.Listen = tt.listen
			o.Routes = []func(*http.ServeMux){func(mux *http.ServeMux) {
				mux.HandleFunc("/extra", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
			}}
			h := Handler(cfg, o)
			for _, path := range []string{"/healthz", "/extra", "/mcp", "/.well-known/oauth-protected-resource", "/oauth/register", "/admin/clients", "/missing"} {
				for _, method := range []string{"GET", "POST", "OPTIONS"} {
					r := httptest.NewRequest(method, path, nil)
					r.Host = tt.host
					// Forwarded headers are never authority for the Host decision.
					r.Header.Set("X-Forwarded-Host", "mcp.example.com")
					r.Header.Set("Forwarded", "host=mcp.example.com")
					if tt.local != "" {
						addr, err := net.ResolveTCPAddr("tcp", tt.local)
						if err != nil {
							t.Fatal(err)
						}
						r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, addr))
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if (w.Code != 421) != tt.allowed {
						t.Fatalf("%s %s status=%d", method, path, w.Code)
					}
					if !tt.allowed && w.Header().Get("Access-Control-Allow-Origin") != "" {
						t.Fatal("foreign host reached CORS")
					}
				}
			}
		})
	}
}

func TestPublicHostPortIsExact(t *testing.T) {
	cfg, o := testOptions(t)
	cfg.HTTP.Listen = "0.0.0.0:8787"
	cfg.HTTP.PublicURL = "https://mcp.example.com:8443"
	h := Handler(cfg, o)
	for _, host := range []string{"mcp.example.com:8443", "mcp.example.com", "mcp.example.com:443"} {
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if (w.Code == 200) != (host == "mcp.example.com:8443") {
			t.Fatalf("host=%s status=%d", host, w.Code)
		}
	}
}

func TestReverseProxyPreservesPublicHost(t *testing.T) {
	cfg, o := testOptions(t)
	cfg.HTTP.Listen = "127.0.0.1:0"
	backend := tcpTestServer(t, Handler(cfg, o))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := tcpTestServer(t, &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme = target.Scheme
		r.Out.URL.Host = target.Host
		// Keep the incoming Host, as in the documented reverse proxy setup.
		r.Out.Host = r.In.Host
	}})
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, proxyURL.Host)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: bearerTransport{base: transport, token: "read-token"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "proxy-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://mcp.example.com/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "static:reader" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/mcp", "/healthz", "/.well-known/oauth-protected-resource", "/oauth/register"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://foreign.example"+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 421 || !strings.Contains(string(body), "misdirected request") {
			t.Fatalf("foreign Host accepted on %s: %d %s", path, resp.StatusCode, body)
		}
	}
}

func tcpTestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("sandbox denies TCP listeners: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: h}}
	server.Start()
	return server
}

func TestReverseProxyOverPipe(t *testing.T) {
	cfg, options := testOptions(t)
	backend := newPipeServer(Handler(cfg, options))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newPipeServer(&httputil.ReverseProxy{
		Transport: backend.Client().Transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = target.Scheme, target.Host
			r.Out.Host = r.In.Host
		},
	})
	defer proxy.Close()
	client := proxy.Client()
	client.Transport = bearerTransport{base: client.Transport, token: "read-token"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "proxy-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: proxy.URL + "/mcp", HTTPClient: client}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != "static:reader" {
		t.Fatalf("unexpected result: %+v", result)
	}
	req, _ := http.NewRequestWithContext(ctx, "OPTIONS", proxy.URL+"/mcp", nil)
	req.Host = "foreign.example"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host accepted: %d", resp.StatusCode)
	}
}
