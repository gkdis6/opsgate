package approval

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestFullReviewRejectsInput(t *testing.T) {
	m, _ := newWebManager(t)
	r := approvalRequest()
	state := inputRequest(t, m, r)
	r.FullReviewRequired = true
	approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
	if _, err := confirmResponse(m, state, approved, r); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("accept replayed for truncated plan: %v", err)
	}
}

func TestFullReviewInputBinding(t *testing.T) {
	m, _ := newWebManager(t)
	r := approvalRequest()
	r.FullReviewRequired = true
	// Issue signed state directly to test binding even though Confirm must
	// never issue elicitation for a plan requiring full review.
	err := m.requestInput(r, r.ArgsHash)
	var input *tool.InputRequired
	if !errors.As(err, &input) {
		t.Fatal(err)
	}
	encoded, _, _ := strings.Cut(input.State, ".")
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(payload, &state); err != nil || state["full_review_required"] != true {
		t.Errorf("full review requirement missing from signed state: %s (%v)", payload, err)
	}
	r.FullReviewRequired = false
	approved := &mcp.ElicitResult{Action: "accept", Content: map[string]any{"approve": true}}
	if _, err := confirmResponse(m, input.State, approved, r); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("removed full review requirement accepted: %v", err)
	}
}

func TestFullReviewWebBinding(t *testing.T) {
	for _, full := range []bool{false, true} {
		m, mux := newWebManager(t)
		r := approvalRequest()
		r.FullReviewRequired = full
		id := pendingID(t, m, r)
		decide(t, mux, id, "approve")
		r.ApprovalID, r.FullReviewRequired = id, !full
		if method, err := m.Confirm(context.Background(), nil, r); method != "web" || !errors.Is(err, tool.ErrDenied) {
			t.Fatalf("changed full review requirement accepted: %s %v", method, err)
		}
	}
}

func TestFullReviewWindow(t *testing.T) {
	m, mux := windowManager(windowConfig(), nil)
	r := approvalRequest()
	r.FullReviewRequired = true
	id := pendingID(t, m, r)
	decide(t, mux, id, "approve_window")
	// Existing grants still cover any valid arguments across transports.
	m.browser = nil
	r.Caller.Transport = "stdio"
	if method, err := m.Confirm(context.Background(), nil, r); method != "window" || err != nil {
		t.Fatalf("full review changed window behavior: %s %v", method, err)
	}
	r.NoWindow = true
	if _, err := m.Confirm(context.Background(), nil, r); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("NoWindow bypassed: %v", err)
	}
}

func TestFullReviewOpensArguments(t *testing.T) {
	for _, full := range []bool{false, true} {
		m, mux := newWebManager(t)
		r := approvalRequest()
		r.FullReviewRequired = full
		id := pendingID(t, m, r)
		body := webRequest(mux, http.MethodGet, id, r.Caller.User, nil, nil).Body.String()
		if strings.Contains(body, "<details open>") != full {
			t.Errorf("full review %t: arguments open state wrong", full)
		}
	}
}
