package approval

import (
	"os"
	"path/filepath"

	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestCapacityAndExpiry(t *testing.T) {
	m, mux := newWebManager(t)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	var first string
	for range 100 {
		id := pendingID(t, m, approvalRequest())
		if first == "" {
			first = id
		}
	}
	if _, err := m.Confirm(context.Background(), nil, approvalRequest()); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("over capacity = %v", err)
	}
	if len(m.approvals) != 100 {
		t.Fatalf("records = %d", len(m.approvals))
	}
	now = now.Add(time.Minute)
	w := webRequest(mux, "GET", first, "github:operator", nil, nil)
	if w.Code != http.StatusNotFound || len(m.approvals) != 0 {
		t.Fatalf("expired GET = %d; records %d", w.Code, len(m.approvals))
	}
	pendingID(t, m, approvalRequest())
	if len(m.approvals) != 1 {
		t.Errorf("capacity not reclaimed: %d", len(m.approvals))
	}
}

func TestCanonicalArgs(t *testing.T) {
	m, mux := newWebManager(t)
	r := approvalRequest()
	r.Args = map[string]any{"z": 2, "a": map[string]any{"second": "two", "first": "one"}}
	id := pendingID(t, m, r)
	// The stored snapshot must not alias a caller-owned map.
	r.Args["a"].(map[string]any)["first"] = "changed"
	r.Args = map[string]any{"a": map[string]any{"first": "one", "second": "two"}, "z": 2}
	r.ApprovalID = id
	decide(t, mux, id, "approve")
	if method, err := m.Confirm(context.Background(), nil, hashedRequest(r)); err != nil || method != "web" {
		t.Errorf("same canonical arguments = %q, %v", method, err)
	}
	r.ApprovalID = ""
	r.Args = map[string]any{"n": json.Number("9007199254740992")}
	r.ApprovalID = pendingID(t, m, r)
	decide(t, mux, r.ApprovalID, "approve")
	r.Args["n"] = json.Number("9007199254740993")
	if _, err := m.Confirm(context.Background(), nil, hashedRequest(r)); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("distinct integer allowed: %v", err)
	}
}

func TestInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*tool.ConfirmRequest)
		ttl    time.Duration
		cancel bool
	}{
		{name: "zero TTL", ttl: 0},
		{name: "negative TTL", ttl: -time.Second},
		{name: "no subject", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.Caller.User = "" }},
		{name: "no summary", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.Summary = "" }},
		{name: "invalid approval id", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.ApprovalID = "invalid" }},
		{name: "large summary", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.Summary = strings.Repeat("a", 64<<10+1) }},
		{name: "large args", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.Args["large"] = strings.Repeat("a", 64<<10) }},
		{name: "invalid args", ttl: time.Minute, mutate: func(r *tool.ConfirmRequest) { r.Args["invalid"] = make(chan int) }},
		{name: "cancelled", ttl: time.Minute, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(config.Approval{TTL: tc.ttl}, "https://ops.example", fakeBrowser{}, nil)
			r := approvalRequest()
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			_, err := m.Confirm(ctx, nil, hashedRequest(r))
			if err == nil || len(m.approvals) != 0 {
				t.Fatalf("invalid request created approval: %v", err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) || !tc.cancel && !errors.Is(err, tool.ErrDenied) {
				t.Errorf("error = %v", err)
			}
		})
	}
}

func TestWebDisabled(t *testing.T) {
	m := New(config.Approval{TTL: time.Minute}, "https://ops.example", nil, nil)
	mux := http.NewServeMux()
	m.Routes(mux)
	w := webRequest(mux, "GET", strings.Repeat("a", 26), "github:operator", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("disabled route = %d", w.Code)
	}
	if _, err := m.Confirm(context.Background(), nil, approvalRequest()); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("disabled approval = %v", err)
	}
}

func TestCSRFIsPerApproval(t *testing.T) {
	m, mux := newWebManager(t)
	id1 := pendingID(t, m, approvalRequest())
	id2 := pendingID(t, m, approvalRequest())
	csrf := readCSRF(t, mux, id1)
	w := webRequest(mux, "POST", id2, "github:operator", map[string][]string{"csrf": {csrf}, "decision": {"approve"}}, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("another approval's CSRF token accepted: %d", w.Code)
	}
}

func TestRequireWebConfig(t *testing.T) {
	cfg := config.Default()
	cfg.HTTP.PublicURL = "https://ops.example"
	cfg.Auth.Mode = config.AuthBuiltin
	cfg.Approval.RequireWeb = true
	if err := cfg.ValidateHTTP(); err == nil || !strings.Contains(err.Error(), "require_web requires approval.web") {
		t.Fatalf("require_web without web: %v", err)
	}
	cfg.Approval.Web = true
	if err := cfg.ValidateHTTP(); err != nil {
		t.Fatal(err)
	}
}

func TestRequireWebYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("approval:\n  web: true\n  require_web: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || !cfg.Approval.RequireWeb || !cfg.Approval.Web {
		t.Fatalf("config = %+v; err %v", cfg.Approval, err)
	}
}
