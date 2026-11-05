package analyzer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPReadOnlyWire(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "k8s_get", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "OOMKilled"}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "k8s_rollout_restart", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Error("write reached server")
		return &mcp.CallToolResult{}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer reader-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c := Default()
	c.MCP.Endpoint = srv.URL + "/mcp"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := connect(ctx, c, "reader-token")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tools, err := s.List(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "k8s_get" {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	if _, err := s.Call(ctx, "k8s_rollout_restart", map[string]any{}); err == nil {
		t.Fatal("write accepted")
	}
	out, err := s.Call(ctx, "k8s_get", map[string]any{})
	if err != nil || out.IsError {
		t.Fatal(err)
	}
}
