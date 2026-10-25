package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func approvalRequest() tool.ConfirmRequest {
	return tool.ConfirmRequest{
		Caller: tool.Caller{User: "github:operator", Transport: "http", Scopes: []string{config.ScopeWrite}},
		Tool:   "restart", Summary: "Restart nginx.service", Args: map[string]any{"unit": "nginx.service"},
	}
}

func connectApproval(t *testing.T, s *mcp.Server, opts *mcp.ClientOptions, versions ...string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, opts)
	version := "2025-11-25"
	if len(versions) > 0 {
		version = versions[0]
	}
	cs, err := c.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestElicitation(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		approve      any
		noCapability bool
		newProtocol  bool
		clientErr    error
		timeout, web bool
		wantMethod   string
		wantDenied   bool
		wantPending  bool
		wantMessage  string
	}{
		{name: "accept", action: "accept", approve: true, wantMethod: "elicitation"},
		{name: "decline", action: "decline", wantMethod: "elicitation", wantDenied: true, wantMessage: "rejected by human"},
		{name: "cancel", action: "cancel", wantMethod: "elicitation", wantDenied: true, wantMessage: "rejected by human"},
		{name: "false", action: "accept", approve: false, wantMethod: "elicitation", wantDenied: true, wantMessage: "rejected by human"},
		{name: "missing approve", action: "accept", wantMethod: "elicitation", wantDenied: true, wantMessage: "rejected by human"},
		{name: "wrong type", action: "accept", approve: "true", wantDenied: true, wantMessage: "no human confirmation channel"},
		{name: "no capability", noCapability: true, wantDenied: true, wantMessage: "no human confirmation channel"},
		{name: "elicitation error", clientErr: errors.New("client error password=hidden"), wantDenied: true, wantMessage: "no human confirmation channel"},
		{name: "error falls back to web", clientErr: errors.New("client error"), web: true, wantMethod: "web", wantPending: true},
		{name: "no capability falls back to web", noCapability: true, web: true, wantMethod: "web", wantPending: true},
		{name: "decline never falls back", action: "decline", web: true, wantMethod: "elicitation", wantDenied: true, wantMessage: "rejected by human"},
		{name: "timeout", timeout: true, wantDenied: true, wantMessage: "no human confirmation channel"},
		{name: "timeout falls back to web", timeout: true, web: true, wantMethod: "web", wantPending: true},
		{name: "new protocol denies server elicitation", newProtocol: true, wantDenied: true, wantMessage: "no human confirmation channel"},
		{name: "new protocol web fallback", newProtocol: true, web: true, wantMethod: "web", wantPending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var browser BrowserAuth
			if tc.web {
				browser = fakeBrowser{}
			}
			ttl := 5 * time.Second
			if tc.timeout {
				ttl = 250 * time.Millisecond
			}
			m := New(config.Approval{TTL: ttl}, "https://ops.example", browser)
			var elicited atomic.Int32
			opts := &mcp.ClientOptions{}
			if !tc.noCapability {
				opts.ElicitationHandler = func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					elicited.Add(1)
					want := "opsgate approval request\n\nRestart nginx.service\n\nRequested by github:operator via http. Approve?"
					if req.Params.Mode != "form" || req.Params.Message != want {
						t.Errorf("elicitation params = %+v", req.Params)
					}
					data, err := json.Marshal(req.Params.RequestedSchema)
					if err != nil {
						t.Error(err)
					}
					var schema struct {
						Type       string
						Required   []string
						Properties map[string]struct{ Type, Title string }
					}
					if err := json.Unmarshal(data, &schema); err != nil {
						t.Error(err)
					}
					if schema.Type != "object" || len(schema.Required) != 1 || schema.Required[0] != "approve" || len(schema.Properties) != 1 ||
						schema.Properties["approve"].Type != "boolean" || schema.Properties["approve"].Title != "Approve this action" {
						t.Errorf("schema = %s", data)
					}
					if tc.timeout {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					if tc.clientErr != nil {
						return nil, tc.clientErr
					}
					result := &mcp.ElicitResult{Action: tc.action}
					if tc.approve != nil {
						result.Content = map[string]any{"approve": tc.approve}
					}
					return result, nil
				}
			}
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			mcp.AddTool(s, &mcp.Tool{Name: "restart"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				method, err := m.Confirm(ctx, req, approvalRequest())
				if method != tc.wantMethod || errors.Is(err, tool.ErrDenied) != tc.wantDenied || errors.Is(err, tool.ErrApprovalPending) != tc.wantPending {
					t.Errorf("Confirm = %q, %v", method, err)
				}
				if tc.wantMessage != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMessage)) {
					t.Errorf("Confirm error = %v, want %q", err, tc.wantMessage)
				}
				if err != nil && strings.Contains(err.Error(), "hidden") {
					t.Error("client error leaked")
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprint(err)}}}, nil, nil
			})
			version := "2025-11-25"
			if tc.newProtocol {
				version = "2026-07-28"
			}
			cs := connectApproval(t, s, opts, version)
			if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			wantElicited := int32(1)
			if tc.noCapability || tc.newProtocol {
				wantElicited = 0
			}
			if elicited.Load() != wantElicited {
				t.Errorf("elicitation handler called %d times, want %d", elicited.Load(), wantElicited)
			}
		})
	}
}
