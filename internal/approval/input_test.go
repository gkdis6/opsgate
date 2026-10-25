package approval

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func manualInputOptions() *mcp.ClientOptions {
	return &mcp.ClientOptions{
		Capabilities:   &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{}},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	}
}

func acceptedInput(state string) *mcp.CallToolParams {
	return &mcp.CallToolParams{
		Name: "restart", Arguments: map[string]any{"unit": "nginx.service"}, RequestState: state,
		InputResponses: mcp.InputResponseMap{approvalInputID: &mcp.ElicitResult{
			Action: "accept", Content: map[string]any{"approve": true},
		}},
	}
}

func flipStateByte(t *testing.T, state string, part int) string {
	t.Helper()
	parts := strings.Split(state, ".")
	b, err := base64.RawURLEncoding.DecodeString(parts[part])
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 1
	parts[part] = base64.RawURLEncoding.EncodeToString(b)
	return strings.Join(parts, ".")
}

func TestInputStateSecurity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*testing.T, *mcp.CallToolParams)
		binding    func(*tool.ConfirmRequest)
		replay     bool
		expired    bool
		newManager bool
	}{
		{name: "tampered payload", mutate: func(t *testing.T, p *mcp.CallToolParams) { p.RequestState = flipStateByte(t, p.RequestState, 0) }},
		{name: "tampered MAC", mutate: func(t *testing.T, p *mcp.CallToolParams) { p.RequestState = flipStateByte(t, p.RequestState, 1) }},
		{name: "missing state", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.RequestState = "" }},
		{name: "oversized state", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.RequestState = strings.Repeat("a", maxStateBytes+1) }},
		{name: "invalid base64", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.RequestState = "!.!" }},
		{name: "replay", replay: true},
		{name: "different arguments", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.Arguments = map[string]any{"unit": "other.service"} }},
		{name: "different user", binding: func(r *tool.ConfirmRequest) { r.Caller.User = "github:other" }},
		{name: "different transport", binding: func(r *tool.ConfirmRequest) { r.Caller.Transport = "stdio" }},
		{name: "different tool", binding: func(r *tool.ConfirmRequest) { r.Tool = "delete" }},
		{name: "different summary", binding: func(r *tool.ConfirmRequest) { r.Summary = "Delete nginx.service" }},
		{name: "expired", expired: true},
		{name: "another manager", newManager: true},
		{name: "state without response", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.InputResponses = nil }},
		{name: "unknown response ID", mutate: func(_ *testing.T, p *mcp.CallToolParams) {
			p.InputResponses["other"] = p.InputResponses[approvalInputID]
			delete(p.InputResponses, approvalInputID)
		}},
		{name: "wrong response type", mutate: func(_ *testing.T, p *mcp.CallToolParams) { p.InputResponses[approvalInputID] = &mcp.ListRootsResult{} }},
		{name: "wrong approve type", mutate: func(_ *testing.T, p *mcp.CallToolParams) {
			p.InputResponses[approvalInputID] = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": "true"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(config.Approval{TTL: time.Minute}, "https://ops.example", nil)
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			m.now = func() time.Time { return time.Unix(0, now.Load()) }
			source, sourceApplies, _ := newApprovalTool(t, m, approvalRequest())
			cs := connectApproval(t, source, manualInputOptions(), "2026-07-28")
			initial := approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": "nginx.service"}})
			if !initial.NeedsInput() || len(initial.Content) != 0 || initial.RequestState == "" || sourceApplies.Load() != 0 {
				t.Fatalf("initial result = %+v; applies = %d", initial, sourceApplies.Load())
			}
			if tc.replay {
				if r := approvalCall(t, cs, acceptedInput(initial.RequestState)); r.IsError || sourceApplies.Load() != 1 {
					t.Fatalf("initial approval = %+v; applies = %d", r, sourceApplies.Load())
				}
			}
			if tc.expired {
				now.Add(int64(time.Minute))
			}
			if tc.newManager {
				m = New(config.Approval{TTL: time.Minute}, "https://ops.example", nil)
			}
			r := approvalRequest()
			if tc.binding != nil {
				tc.binding(&r)
			}
			target, applies, path := newApprovalTool(t, m, r)
			client := connectApproval(t, target, manualInputOptions(), "2026-07-28")
			params := acceptedInput(initial.RequestState)
			params.Name = r.Tool
			if tc.mutate != nil {
				tc.mutate(t, params)
			}
			result := approvalCall(t, client, params)
			if !result.IsError || result.NeedsInput() || applies.Load() != 0 {
				t.Fatalf("invalid input result = %+v; applies = %d", result, applies.Load())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), initial.RequestState) || strings.Contains(string(data), params.RequestState) && params.RequestState != "" {
				t.Error("request state leaked to audit")
			}
		})
	}
}

func inputRequest(t *testing.T, m *Manager, r tool.ConfirmRequest) string {
	t.Helper()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: mcp.Meta{mcp.MetaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{}}},
	}}
	method, err := m.Confirm(context.Background(), req, r)
	var input *tool.InputRequired
	if method != "elicitation" || !errors.As(err, &input) {
		t.Fatalf("Confirm = %s, %v", method, err)
	}
	return input.State
}

func confirmResponse(m *Manager, state string, response mcp.InputResponse, r tool.ConfirmRequest) (string, error) {
	return m.Confirm(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		RequestState: state, InputResponses: mcp.InputResponseMap{approvalInputID: response},
	}}, r)
}

func TestInputNonceConsumedOnEveryDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response mcp.InputResponse
		binding  bool
	}{
		{name: "decline", response: &mcp.ElicitResult{Action: "decline"}},
		{name: "cancel", response: &mcp.ElicitResult{Action: "cancel"}},
		{name: "false", response: &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": false}}},
		{name: "nil response"},
		{name: "typed nil response", response: (*mcp.ElicitResult)(nil)},
		{name: "wrong type", response: &mcp.ListRootsResult{}},
		{name: "binding mismatch", binding: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(config.Approval{TTL: time.Minute}, "", nil)
			r := approvalRequest()
			state := inputRequest(t, m, r)
			if tc.binding {
				r.Caller.User = "another user"
			}
			if _, err := confirmResponse(m, state, tc.response, r); !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("invalid response = %v", err)
			}
			approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
			if _, err := confirmResponse(m, state, approved, approvalRequest()); !errors.Is(err, tool.ErrDenied) {
				t.Errorf("replay after invalid response = %v", err)
			}
		})
	}
}

func TestInputConcurrentConsume(t *testing.T) {
	m := New(config.Approval{TTL: time.Minute}, "", nil)
	r := approvalRequest()
	state := inputRequest(t, m, r)
	approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := confirmResponse(m, state, approved, r); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, tool.ErrDenied) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Errorf("approval consumed %d times", successes.Load())
	}
}

func TestInputCanonicalArgs(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "distinct integer"}[changed], func(t *testing.T) {
			m := New(config.Approval{TTL: time.Minute}, "", nil)
			r := approvalRequest()
			r.Args = map[string]any{"z": json.Number("9007199254740992"), "a": map[string]any{"second": "two", "first": "one"}}
			state := inputRequest(t, m, r)
			r.Args = map[string]any{"a": map[string]any{"first": "one", "second": "two"}, "z": json.Number("9007199254740992")}
			if changed {
				r.Args["z"] = json.Number("9007199254740993")
			}
			_, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, r)
			if errors.Is(err, tool.ErrDenied) != changed || !changed && err != nil {
				t.Errorf("Confirm = %v", err)
			}
		})
	}
}

func TestInputCapacityAndExpiry(t *testing.T) {
	m := New(config.Approval{TTL: time.Minute}, "", nil)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	r := approvalRequest()
	states := make([]string, maxUsedNonces+1)
	for i := range states {
		states[i] = inputRequest(t, m, r)
	}
	approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
	for _, state := range states[:maxUsedNonces] {
		if _, err := confirmResponse(m, state, approved, r); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.used) != maxUsedNonces {
		t.Fatalf("used nonces = %d", len(m.used))
	}
	if _, err := confirmResponse(m, states[maxUsedNonces], approved, r); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("overflowing replay cache = %v", err)
	}
	if _, err := confirmResponse(m, states[0], approved, r); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("replay after capacity reached = %v", err)
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: mcp.Meta{mcp.MetaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{}}},
	}}
	if _, err := m.Confirm(context.Background(), req, r); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("issuing state at capacity = %v", err)
	}
	now = now.Add(time.Minute)
	inputRequest(t, m, r)
	if len(m.used) != 0 {
		t.Errorf("expired nonces not swept: %d", len(m.used))
	}
	if _, err := confirmResponse(m, states[0], approved, r); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("expired replay after sweep = %v", err)
	}
}
