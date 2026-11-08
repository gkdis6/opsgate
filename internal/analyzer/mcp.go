package analyzer

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Explicit names supplement readOnlyHint; a misleading annotation cannot enable writes.
var readTools = []string{"k8s_clusters", "k8s_overview", "k8s_list", "k8s_get", "k8s_events", "k8s_logs", "k8s_service_check", "k8s_remediation_options", "action_link", "note_search", "note_read", "note_recent", "note_list", "note_links", "note_tags"}

type toolSession interface {
	List(context.Context) ([]*mcp.Tool, error)
	Call(context.Context, string, map[string]any) (*mcp.CallToolResult, error)
	Close() error
}

type session struct{ *mcp.ClientSession }

func (s session) List(ctx context.Context) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for t, err := range s.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("MCP tool listing failed")
		}
		if slices.Contains(readTools, t.Name) && t.Annotations != nil && t.Annotations.ReadOnlyHint {
			tools = append(tools, t)
		}
		if len(tools) > len(readTools) {
			return nil, fmt.Errorf("duplicate MCP tools")
		}
	}
	return tools, nil
}
func (s session) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if !slices.Contains(readTools, name) {
		return nil, fmt.Errorf("tool is not allowed")
	}
	return s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func connect(ctx context.Context, c Config, token string) (toolSession, error) {
	client := httpClient(c.Timeout)
	client.Transport = bearerTransport{http.DefaultTransport, token}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "opsgate-analyzer", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.MCP.Endpoint, HTTPClient: client}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		return nil, fmt.Errorf("MCP connection failed")
	}
	return session{cs}, nil
}
