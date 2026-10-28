package approval

import (
	"errors"
	"net/http"

	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func approvalRequest() tool.ConfirmRequest {
	return hashedRequest(tool.ConfirmRequest{
		Caller: tool.Caller{User: "github:operator", Transport: "http", Scopes: []string{config.ScopeWrite}},
		Tool:   "restart", Summary: "Restart nginx.service", Args: map[string]any{"unit": "nginx.service"},
	})
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

func newApprovalTool(t *testing.T, m *Manager, r tool.ConfirmRequest) (*mcp.Server, *atomic.Int32, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	env := &tool.Env{Confirm: m, Audit: logger, Timeout: time.Second, MaxOutputBytes: 4096, Fallback: &r.Caller}
	applies := new(atomic.Int32)
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	tool.AddWrite(s, env, tool.WriteSpec{Name: r.Tool}, tool.WriteHandler[restartInput]{
		Plan: func(context.Context, tool.Caller, restartInput) (tool.Plan, error) {
			return tool.Plan{Summary: r.Summary, Target: r.Target, NoWindow: r.NoWindow}, nil
		},
		Apply: func(context.Context, tool.Caller, restartInput, tool.Plan) (string, error) {
			applies.Add(1)
			return "restarted", nil
		},
	})
	return s, applies, path
}

func approvalCall(t *testing.T, cs *mcp.ClientSession, p *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := cs.CallTool(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestElicitation(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			for _, tc := range []struct {
				name, action string
				approve      any
				noCapability bool
				web          bool
				wantDenied   bool
			}{
				{name: "accept", action: "accept", approve: true},
				{name: "decline", action: "decline", wantDenied: true},
				{name: "cancel", action: "cancel", wantDenied: true},
				{name: "false", action: "accept", approve: false, wantDenied: true},
				{name: "missing approve", action: "accept", wantDenied: true},
				{name: "no capability", noCapability: true, wantDenied: true},
				{name: "no capability web", noCapability: true, web: true},
				{name: "decline with web", action: "decline", web: true, wantDenied: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var browser BrowserAuth
					if tc.web {
						browser = fakeBrowser{}
					}
					m := New(config.Approval{TTL: time.Minute}, "https://ops.example", browser, nil)
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
							result := &mcp.ElicitResult{Action: tc.action}
							if tc.approve != nil {
								result.Content = map[string]any{"approve": tc.approve}
							}
							return result, nil
						}
					}
					s, applies, path := newApprovalTool(t, m, approvalRequest())
					// Disabling client retries for the old protocol proves that the
					// SDK's server middleware performs the elicitation and retry.
					if version == "2025-11-25" {
						opts.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
					}
					cs := connectApproval(t, s, opts, version)
					if got := cs.InitializeResult().ProtocolVersion; got != version {
						t.Fatalf("protocol = %s, want %s", got, version)
					}
					params := &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "nginx.service"}}
					result := approvalCall(t, cs, params)
					wantApply := int32(0)
					if !tc.wantDenied && !tc.noCapability {
						wantApply = 1
					}
					if result.IsError != tc.wantDenied || applies.Load() != wantApply || result.NeedsInput() {
						t.Fatalf("result = %+v; applies = %d", result, applies.Load())
					}
					if !tc.noCapability && tc.wantDenied && !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "rejected by human") {
						t.Errorf("denial = %+v", result)
					}
					if tc.noCapability && tc.web && !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "/approve/") {
						t.Errorf("web instructions = %+v", result)
					}
					wantElicited := int32(1)
					if tc.noCapability {
						wantElicited = 0
					}
					if elicited.Load() != wantElicited || len(params.InputResponses) != 0 || params.RequestState != "" {
						t.Errorf("elicited = %d; original params = %+v", elicited.Load(), params)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
					if !tc.noCapability {
						if len(lines) != 2 {
							t.Fatalf("audit = %s", data)
						}
						var pending audit.Event
						if err := json.Unmarshal(lines[0], &pending); err != nil {
							t.Fatal(err)
						}
						if pending.Outcome != "pending" || pending.Approval != "elicitation" || pending.OutputSize != 0 || pending.Error != "" {
							t.Errorf("pending audit = %+v", pending)
						}
					}
					var last audit.Event
					if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
						t.Fatal(err)
					}
					wantOutcome := "ok"
					if tc.wantDenied {
						wantOutcome = "denied"
					} else if tc.noCapability {
						wantOutcome = "pending"
					}
					if last.Outcome != wantOutcome {
						t.Errorf("final audit = %+v", last)
					}
				})
			}
		})
	}
}

// hashedRequest models AddWrite's binding of unredacted fixture arguments.
func hashedRequest(r tool.ConfirmRequest) tool.ConfirmRequest {
	b, _ := json.Marshal(r.Args)
	r.ArgsHash = sha256.Sum256(b)
	return r
}

func TestRequireWeb(t *testing.T) {
	for _, transport := range []string{"http", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			var browser BrowserAuth
			if transport == "http" {
				browser = fakeBrowser{}
			}
			m := New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true}, "https://ops.example", browser, nil)
			r := approvalRequest()
			r.Caller.Transport = transport
			s, applies, _ := newApprovalTool(t, m, r)
			var elicited atomic.Int32
			cs := connectApproval(t, s, &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				elicited.Add(1)
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, nil
			}})
			result := approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "nginx.service"}})
			text := result.Content[0].(*mcp.TextContent).Text
			if applies.Load() != 0 || elicited.Load() != 0 || result.NeedsInput() {
				t.Fatalf("result = %+v; applies %d elicited %d", result, applies.Load(), elicited.Load())
			}
			if transport == "http" {
				if result.IsError || !strings.Contains(text, "/approve/") {
					t.Fatal(text)
				}
				mux := http.NewServeMux()
				m.Routes(mux)
				id := linkID.FindStringSubmatch(text)[1]
				decide(t, mux, id, "approve")
				result = approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "nginx.service", "approval_id": id}})
				if result.IsError || applies.Load() != 1 || elicited.Load() != 0 {
					t.Fatalf("approved result = %+v; applies %d elicited %d", result, applies.Load(), elicited.Load())
				}
			} else if !result.IsError || !strings.Contains(text, "browser approval is required") {
				t.Fatal(text)
			}
		})
	}
}

func TestRequireWebRejectsInputState(t *testing.T) {
	m := New(config.Approval{TTL: time.Minute, Web: true}, "https://ops.example", fakeBrowser{}, nil)
	r := approvalRequest()
	state := inputRequest(t, m, r)
	m.requireWeb = true
	_, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, r)
	if !errors.Is(err, tool.ErrDenied) || !strings.Contains(err.Error(), "browser approval is required") {
		t.Fatal(err)
	}
}

func TestRedactedArgumentBinding(t *testing.T) {
	for _, method := range []string{"web", "elicitation"} {
		t.Run(method, func(t *testing.T) {
			m, mux := newWebManager(t)
			s, applies, _ := newApprovalTool(t, m, approvalRequest())
			var opts *mcp.ClientOptions
			if method == "elicitation" {
				opts = manualInputOptions()
			}
			cs := connectApproval(t, s, opts, "2026-07-28")
			first := approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "glpat-aaaaaaaa"}})
			params := &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "glpat-bbbbbbbb"}}
			if method == "web" {
				id := linkID.FindStringSubmatch(first.Content[0].(*mcp.TextContent).Text)[1]
				decide(t, mux, id, "approve")
				params.Arguments = map[string]any{"unit": "glpat-bbbbbbbb", "approval_id": id}
			} else {
				if !first.NeedsInput() {
					t.Fatalf("initial result = %+v", first)
				}
				params.RequestState = first.RequestState
				params.InputResponses = mcp.InputResponseMap{approvalInputID: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}}
			}
			result := approvalCall(t, cs, params)
			if !result.IsError || result.NeedsInput() || applies.Load() != 0 {
				t.Fatalf("colliding target accepted: %+v; applies %d", result, applies.Load())
			}
		})
	}
}
