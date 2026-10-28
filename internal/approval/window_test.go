package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func windowConfig() config.Approval {
	return config.Approval{TTL: time.Minute, Web: true, Window: 30 * time.Minute, WindowTools: []string{"rest*"}}
}

func windowManager(cfg config.Approval, logger *audit.Logger) (*Manager, *http.ServeMux) {
	m := New(cfg, "https://ops.example", fakeBrowser{}, logger)
	mux := http.NewServeMux()
	m.Routes(mux)
	return m, mux
}

func TestWebWindowBindingAndLifetime(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		change                   func(*tool.ConfirmRequest)
		expire, restart, allowed bool
	}{
		{name: "same user and tool different arguments", allowed: true, change: func(r *tool.ConfirmRequest) {
			r.Args["unit"] = "other.service"
			r.Summary = "Restart other.service"
		}},
		{name: "different tool also eligible", change: func(r *tool.ConfirmRequest) { r.Tool = "restore" }},
		{name: "different user", change: func(r *tool.ConfirmRequest) { r.Caller.User = "github:other" }},
		{name: "expiry", expire: true},
		{name: "restart", restart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			r := approvalRequest()
			id := pendingID(t, m, r)
			decide(t, mux, id, "approve_window")
			if len(m.grants) != 1 {
				t.Fatal("no grant created")
			}
			if tc.expire {
				now = now.Add(30 * time.Minute)
			}
			if tc.restart {
				m, _ = windowManager(windowConfig(), nil)
			}
			if tc.change != nil {
				tc.change(&r)
			}
			method, err := m.Confirm(context.Background(), nil, hashedRequest(r))
			if tc.allowed {
				if err != nil || method != "window" {
					t.Fatalf("grant not used: %s, %v", method, err)
				}
			} else if method != "web" || !errors.Is(err, tool.ErrApprovalPending) {
				t.Fatalf("unrelated or stale grant used: %s, %v", method, err)
			}
			if tc.expire && len(m.grants) != 0 {
				t.Fatal("expired grant was not swept")
			}
		})
	}
}

func TestWindowEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   time.Duration
		patterns []string
	}{
		{"disabled", 0, []string{"*"}},
		{"empty allowlist", time.Hour, nil},
		{"wrong tool", time.Hour, []string{"delete"}},
		{"invalid pattern", time.Hour, []string{"*", "["}},
		{"invalid duration", time.Second, []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := windowConfig()
			cfg.Window, cfg.WindowTools = tc.window, tc.patterns
			m, mux := windowManager(cfg, nil)
			r := approvalRequest()
			id := pendingID(t, m, r)
			w := webRequest(mux, "GET", id, r.Caller.User, nil, nil)
			if strings.Contains(w.Body.String(), "approve_window") {
				t.Fatal("ineligible window offered on web")
			}
			csrf := readCSRF(t, mux, id)
			w = webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {"approve_window"}}, nil)
			if w.Code != http.StatusBadRequest || len(m.grants) != 0 {
				t.Fatal("forged window accepted")
			}
			var input *tool.InputRequired
			if err := m.requestInput(r, r.ArgsHash); !errors.As(err, &input) {
				t.Fatal(err)
			}
			data, _ := json.Marshal(input.Requests)
			if bytes.Contains(data, []byte("allow_window")) {
				t.Fatal("ineligible window offered in elicitation")
			}
			_, err := confirmResponse(m, input.State, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": true}}, r)
			if !errors.Is(err, tool.ErrDenied) || len(m.grants) != 0 {
				t.Fatal("forged elicitation window accepted")
			}
			m.grants[grantKey{r.Caller.User, r.Tool, r.Target}] = time.Now().Add(time.Hour)
			if method, err := m.Confirm(context.Background(), nil, r); method != "web" || !errors.Is(err, tool.ErrApprovalPending) {
				t.Fatalf("ineligible grant honoured: %s %v", method, err)
			}
		})
	}
}

func TestElicitationWindow(t *testing.T) {
	for _, tc := range []struct {
		name              string
		approve, allow    any
		wantGrant, denied bool
	}{
		{"allow", true, true, true, false},
		{"false", true, false, false, false},
		{"omitted", true, nil, false, false},
		{"wrong type", true, "true", false, true},
		{"not approved", false, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := windowManager(windowConfig(), nil)
			r := approvalRequest()
			var input *tool.InputRequired
			if err := m.requestInput(r, r.ArgsHash); !errors.As(err, &input) {
				t.Fatal(err)
			}
			data, _ := json.Marshal(input.Requests[approvalInputID].(*mcp.ElicitParams).RequestedSchema)
			var schema struct {
				Required   []string
				Properties map[string]struct{ Type, Title string }
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if len(schema.Required) != 1 || schema.Required[0] != "approve" || schema.Properties["allow_window"].Type != "boolean" ||
				schema.Properties["allow_window"].Title != "Also auto-approve restart for 30m" {
				t.Fatalf("schema = %s", data)
			}
			content := map[string]any{"approve": tc.approve}
			if tc.allow != nil {
				content["allow_window"] = tc.allow
			}
			method, err := confirmResponse(m, input.State, &mcp.ElicitResult{Action: "accept", Content: content}, r)
			if method != "elicitation" || (err != nil) != tc.denied || (len(m.grants) == 1) != tc.wantGrant {
				t.Fatalf("result %s %v; grants %v", method, err, m.grants)
			}
			if tc.wantGrant {
				r.Args["unit"], r.Summary = "other.service", "Restart other.service"
				if method, err := m.Confirm(context.Background(), nil, hashedRequest(r)); method != "window" || err != nil {
					t.Fatalf("elicitation grant not reusable: %s %v", method, err)
				}
			}
		})
	}
}

func TestWindowRequiresWeb(t *testing.T) {
	cfg := windowConfig()
	cfg.RequireWeb = true
	m, mux := windowManager(cfg, nil)
	r := approvalRequest()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		mcp.MetaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{}},
	}}}
	method, err := m.Confirm(context.Background(), req, r)
	if method != "web" || !errors.Is(err, tool.ErrApprovalPending) || len(m.grants) != 0 {
		t.Fatalf("require_web bypassed: %s %v", method, err)
	}
	id := linkID.FindStringSubmatch(err.Error())[1]
	decide(t, mux, id, "approve_window")
	if method, err := m.Confirm(context.Background(), req, r); method != "window" || err != nil {
		t.Fatalf("web grant not used: %s %v", method, err)
	}
	r.Caller.Transport = "stdio"
	if _, err := m.Confirm(context.Background(), req, r); !errors.Is(err, tool.ErrDenied) {
		t.Fatal("require_web allowed stdio")
	}
}

func TestWindowStillValidatesInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*tool.ConfirmRequest)
	}{
		{"summary cap", func(r *tool.ConfirmRequest) { r.Summary = strings.Repeat("x", 64<<10+1) }},
		{"secret summary", func(r *tool.ConfirmRequest) { r.Summary = "password=secret" }},
		{"bad id", func(r *tool.ConfirmRequest) { r.ApprovalID = "invalid" }},
		{"args cap", func(r *tool.ConfirmRequest) { r.Args["large"] = strings.Repeat("x", 64<<10) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			r := approvalRequest()
			decide(t, mux, pendingID(t, m, r), "approve_window")
			tc.change(&r)
			if method, err := m.Confirm(context.Background(), nil, hashedRequest(r)); method == "window" || !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("invalid request allowed: %s %v", method, err)
			}
		})
	}
}

func TestWindowCapacityAndConcurrentUse(t *testing.T) {
	m, mux := windowManager(windowConfig(), nil)
	now := time.Now()
	m.now = func() time.Time { return now }
	for i := range maxGrants {
		m.grants[grantKey{fmt.Sprint(i), "restart", ""}] = now.Add(time.Minute)
	}
	r := approvalRequest()
	id := pendingID(t, m, r)
	csrf := readCSRF(t, mux, id)
	w := webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {csrf}, "decision": {"approve_window"}}, nil)
	if w.Code != http.StatusServiceUnavailable || len(m.grants) != maxGrants || m.approvals[id].decision != "" {
		t.Fatal("grant cap failed")
	}
	state := inputRequest(t, m, r)
	if _, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": true}}, r); !errors.Is(err, tool.ErrDenied) {
		t.Fatal("elicitation bypassed grant cap")
	}
	now = now.Add(time.Minute)
	decide(t, mux, pendingID(t, m, r), "approve_window")
	if len(m.grants) != 1 {
		t.Fatal("expired grants not reclaimed")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if method, err := m.Confirm(context.Background(), nil, r); method != "window" || err != nil {
				t.Errorf("concurrent use: %s %v", method, err)
			}
		})
	}
	wg.Wait()
}

func TestWindowAudit(t *testing.T) {
	for _, tc := range []struct{ channel, target string }{
		{"web", ""},
		{"web", `Kubernetes cluster "prod-b"`},
		{"elicitation", ""},
		{"elicitation", `Kubernetes cluster "prod-b"`},
	} {
		t.Run(tc.channel+"/"+tc.target, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			logger, err := audit.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { logger.Close() })
			m, mux := windowManager(windowConfig(), logger)
			r := approvalRequest()
			r.Target = tc.target
			if tc.channel == "web" {
				decide(t, mux, pendingID(t, m, r), "approve_window")
			} else {
				state := inputRequest(t, m, r)
				if _, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": true}}, r); err != nil {
					t.Fatal(err)
				}
			}
			s, applies, callAudit := newApprovalTool(t, m, r)
			cs := connectApproval(t, s, nil)
			for _, unit := range []string{"nginx.service", "other.service"} {
				if out := approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": unit}}); out.IsError {
					t.Fatal(out)
				}
			}
			if applies.Load() != 2 {
				t.Fatal("calls not applied")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var grant audit.Event
			if err := json.Unmarshal(bytes.Split(bytes.TrimSpace(data), []byte("\n"))[0], &grant); err != nil {
				t.Fatal(err)
			}
			if grant.Outcome != "window_granted" || grant.Approval != tc.channel || grant.User != r.Caller.User || grant.Tool != r.Tool || grant.Args["window"] != "30m" || grant.Args["expires_at"] == nil {
				t.Fatalf("grant audit: %s", data)
			}
			if target, exists := grant.Args["target"]; tc.target == "" && exists || tc.target != "" && target != tc.target {
				t.Fatalf("grant target: %+v", grant.Args)
			}
			data, err = os.ReadFile(callAudit)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
				var call audit.Event
				if err := json.Unmarshal(line, &call); err != nil || call.Outcome != "ok" || call.Approval != "window" {
					t.Fatalf("per-call audit = %s: %v", line, err)
				}
			}
		})
	}
}

func TestWindowElicitationEndToEnd(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			m, _ := windowManager(windowConfig(), nil)
			s, applies, _ := newApprovalTool(t, m, approvalRequest())
			var prompts atomic.Int32
			cs := connectApproval(t, s, &mcp.ClientOptions{
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					prompts.Add(1)
					return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": true}}, nil
				},
			}, version)
			for _, unit := range []string{"nginx.service", "other.service"} {
				if result := approvalCall(t, cs, &mcp.CallToolParams{Name: "restart", Arguments: map[string]any{"unit": unit}}); result.IsError || result.NeedsInput() {
					t.Fatal(result)
				}
			}
			if prompts.Load() != 1 || applies.Load() != 2 {
				t.Fatalf("prompts %d; applies %d", prompts.Load(), applies.Load())
			}
		})
	}
}
