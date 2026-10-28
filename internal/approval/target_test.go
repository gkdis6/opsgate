package approval

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestTargetWindowBinding(t *testing.T) {
	for _, channel := range []string{"web", "elicitation"} {
		for _, tc := range []struct {
			name, granted, requested string
			allowed                  bool
		}{
			{"same target", "cluster A", "cluster A", true},
			{"different target", "cluster A", "cluster B", false},
			{"empty request", "cluster A", "", false},
			{"empty grant", "", "cluster A", false},
			{"both empty", "", "", true},
		} {
			t.Run(channel+"/"+tc.name, func(t *testing.T) {
				m, mux := windowManager(windowConfig(), nil)
				r := approvalRequest()
				r.Target = tc.granted
				if channel == "web" {
					decide(t, mux, pendingID(t, m, r), "approve_window")
				} else {
					state := inputRequest(t, m, r)
					if _, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": true}}, r); err != nil {
						t.Fatal(err)
					}
				}
				r.Target = tc.requested
				method, err := m.Confirm(context.Background(), nil, r)
				if tc.allowed {
					if method != "window" || err != nil {
						t.Fatalf("matching grant ignored: %s %v", method, err)
					}
				} else if method != "web" || !errors.Is(err, tool.ErrApprovalPending) {
					t.Fatalf("unrelated grant used: %s %v", method, err)
				}
			})
		}
	}
}

func TestTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		valid        bool
	}{
		{"empty", "", true},
		{"UTF-8", `Kubernetes cluster "운영"`, true},
		{"byte limit", strings.Repeat("é", 128), true},
		{"too long", strings.Repeat("é", 128) + "x", false},
		{"newline", "cluster\nprod", false},
		{"carriage return", "cluster\rprod", false},
		{"tab", "cluster\tprod", false},
		{"null", "cluster\x00prod", false},
		{"delete", "cluster\x7fprod", false},
		{"unicode control", "cluster\u0085prod", false},
		{"invalid UTF-8", "cluster\xff", false},
		{"masked", "password=secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newWebManager(t)
			r := approvalRequest()
			r.Target = tc.target
			_, err := m.Confirm(context.Background(), nil, r)
			if tc.valid {
				if !errors.Is(err, tool.ErrApprovalPending) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, tool.ErrDenied) || len(m.approvals) != 0 {
				t.Fatalf("invalid target accepted: %v", err)
			}
		})
	}
}

func TestTargetDisplay(t *testing.T) {
	for _, target := range []string{`Kubernetes cluster "prod-b"`, "cluster <prod>"} {
		t.Run(target, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			r := approvalRequest()
			r.Target = target
			method, err := m.Confirm(context.Background(), nil, r)
			if method != "web" || !errors.Is(err, tool.ErrApprovalPending) || !strings.Contains(err.Error(), target) {
				t.Fatalf("pending target missing: %s %v", method, err)
			}
			id := linkID.FindStringSubmatch(err.Error())[1]
			body := webRequest(mux, "GET", id, r.Caller.User, nil, nil).Body.String()
			escaped := html.EscapeString(target)
			if !strings.Contains(body, "Target: "+escaped) || strings.Index(body, "Target: "+escaped) > strings.Index(body, r.Summary) ||
				!strings.Contains(body, "The window allows restart on "+escaped+" with any valid arguments for your user for the next 30m.") || strings.Contains(body, "<prod>") {
				t.Fatalf("web target missing or unescaped: %s", body)
			}
			var input *tool.InputRequired
			if err := m.requestInput(r, r.ArgsHash); !errors.As(err, &input) {
				t.Fatal(err)
			}
			params := input.Requests[approvalInputID].(*mcp.ElicitParams)
			if !strings.Contains(params.Message, "Target: "+target+"\n\n"+r.Summary) {
				t.Fatalf("elicitation target missing: %s", params.Message)
			}
			data, _ := json.Marshal(params.RequestedSchema)
			var schema struct {
				Properties map[string]struct{ Title string }
			}
			if err := json.Unmarshal(data, &schema); err != nil || schema.Properties["allow_window"].Title != "Also auto-approve restart on "+target+" for 30m" {
				t.Fatalf("window target missing: %s", data)
			}
		})
	}
}

func TestNoWindow(t *testing.T) {
	for _, target := range []string{"", `Kubernetes cluster "prod-b"`} {
		t.Run(target, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			r := approvalRequest()
			r.Target, r.NoWindow = target, true
			id := pendingID(t, m, r)
			body := webRequest(mux, "GET", id, r.Caller.User, nil, nil).Body.String()
			if strings.Contains(body, "approve_window") || !strings.Contains(body, "This target requires approval for every call.") {
				t.Fatalf("protected web page: %s", body)
			}
			w := webRequest(mux, "POST", id, r.Caller.User, url.Values{"csrf": {readCSRF(t, mux, id)}, "decision": {"approve_window"}}, nil)
			if w.Code != http.StatusBadRequest || len(m.grants) != 0 || m.approvals[id].decision != "" {
				t.Fatal("protected web window accepted")
			}
			var input *tool.InputRequired
			if err := m.requestInput(r, r.ArgsHash); !errors.As(err, &input) {
				t.Fatal(err)
			}
			data, _ := json.Marshal(input.Requests)
			if strings.Contains(string(data), "allow_window") {
				t.Fatal("protected elicitation window offered")
			}
			for _, allow := range []bool{false, true} {
				state := inputRequest(t, m, r)
				if _, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true, "allow_window": allow}}, r); !errors.Is(err, tool.ErrDenied) || len(m.grants) != 0 {
					t.Fatalf("protected elicitation window accepted: %v", err)
				}
			}
			state := inputRequest(t, m, r)
			m.grants[grantKey{r.Caller.User, r.Tool, r.Target}] = m.now().Add(time.Hour)
			if method, err := confirmResponse(m, state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}, r); method != "elicitation" || err != nil {
				t.Fatalf("protected elicitation approval: %s %v", method, err)
			}
			decide(t, mux, id, "approve")
			r.ApprovalID = id
			if method, err := m.Confirm(context.Background(), nil, r); method != "web" || err != nil {
				t.Fatalf("protected web approval: %s %v", method, err)
			}
			r.ApprovalID = ""
			if method, err := m.Confirm(context.Background(), nil, r); method != "web" || !errors.Is(err, tool.ErrApprovalPending) {
				t.Fatalf("protected request used grant: %s %v", method, err)
			}
		})
	}
}
