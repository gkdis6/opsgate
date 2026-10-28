package approval

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestInputTargetBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		noWindow bool
		window   bool
		tamper   func(*inputState)
		change   func(*tool.ConfirmRequest)
	}{
		{name: "tampered target", tamper: func(p *inputState) { p.Target = "cluster B" }},
		{name: "tampered noWindow", tamper: func(p *inputState) { p.NoWindow = true }},
		{name: "tampered protected noWindow", noWindow: true, tamper: func(p *inputState) { p.NoWindow = false }},
		{name: "tampered protected window", noWindow: true, window: true, tamper: func(p *inputState) {
			p.NoWindow, p.Window = false, windowConfig().Window
		}},
		{name: "different target", change: func(r *tool.ConfirmRequest) { r.Target = "cluster B" }},
		{name: "empty target", change: func(r *tool.ConfirmRequest) { r.Target = "" }},
		{name: "different noWindow", change: func(r *tool.ConfirmRequest) { r.NoWindow = true }},
		{name: "protected noWindow removed", noWindow: true, change: func(r *tool.ConfirmRequest) { r.NoWindow = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := windowManager(windowConfig(), nil)
			r := approvalRequest()
			r.Target, r.NoWindow = "cluster A", tc.noWindow
			original := r
			state := inputRequest(t, m, r)
			parts := strings.Split(state, ".")
			payload, err := base64.RawURLEncoding.DecodeString(parts[0])
			if err != nil {
				t.Fatal(err)
			}
			var p inputState
			if err := json.Unmarshal(payload, &p); err != nil || p.Target != r.Target || p.NoWindow != r.NoWindow {
				t.Fatalf("missing state binding: %s (%v)", payload, err)
			}
			if tc.tamper != nil {
				tc.tamper(&p)
				r.Target, r.NoWindow = p.Target, p.NoWindow
				payload, err = json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				parts[0] = base64.RawURLEncoding.EncodeToString(payload)
				state = strings.Join(parts, ".")
			}
			if tc.change != nil {
				tc.change(&r)
			}
			approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
			if tc.window {
				approved.Content["allow_window"] = true
			}
			if method, err := confirmResponse(m, state, approved, r); method != "elicitation" || !errors.Is(err, tool.ErrDenied) || len(m.grants) != 0 {
				t.Fatalf("invalid binding accepted: %s %v", method, err)
			}
			if tc.tamper != nil && len(m.used) != 0 {
				t.Fatal("tampered state passed authentication")
			}
			if tc.change != nil {
				if len(m.used) != 1 {
					t.Fatal("mismatched authenticated state was not consumed")
				}
				if _, err := confirmResponse(m, state, approved, original); !errors.Is(err, tool.ErrDenied) {
					t.Fatalf("replayed mismatched state accepted: %v", err)
				}
			}
		})
	}
}
