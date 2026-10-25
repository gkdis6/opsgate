// Package approval obtains human confirmation for write tools, either via
// MCP elicitation inside the client or via a browser approval page.
package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

// BrowserAuth signs a person in for the approval page. Implemented by
// oauth.Server.
type BrowserAuth interface {
	RequireUser(next func(w http.ResponseWriter, r *http.Request, subject string)) http.Handler
}

// Manager implements tool.Confirmer.
type Manager struct {
	mu        sync.Mutex
	ttl       time.Duration
	publicURL string
	browser   BrowserAuth
	approvals map[string]*pending
	now       func() time.Time
}

type pending struct {
	id, csrf, user, transport, tool, summary string
	args                                     []byte
	hash                                     [sha256.Size]byte
	expires                                  time.Time
	decision                                 string
	used                                     bool
}

// New returns a Manager. browser may be nil, which disables web approval.
// publicURL is the origin used to build approval links.
func New(cfg config.Approval, publicURL string, browser BrowserAuth) *Manager {
	return &Manager{
		ttl: cfg.TTL, publicURL: strings.TrimRight(publicURL, "/"), browser: browser,
		approvals: make(map[string]*pending), now: time.Now,
	}
}

// Confirm implements tool.Confirmer.
func (m *Manager) Confirm(ctx context.Context, req *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.ttl <= 0 || r.Caller.User == "" || len(r.Caller.User) > 1024 ||
		len(r.Caller.Transport) > 64 || r.Tool == "" || len(r.Tool) > 256 || r.Summary == "" || len(r.Summary) > 16<<10 {
		return "", tool.Denied("invalid approval request or TTL")
	}
	if r.ApprovalID != "" && !validID(r.ApprovalID) {
		return "web", tool.Denied("invalid or expired approval")
	}
	args, err := json.Marshal(redact.Value(r.Args))
	if err != nil || len(args) > 64<<10 {
		return "", tool.Denied("approval arguments must be JSON of at most 64 KiB")
	}
	r.Summary = redact.String(r.Summary)
	hash := sha256.Sum256(args)
	m.mu.Lock()
	m.sweepLocked()
	if r.ApprovalID != "" {
		defer m.mu.Unlock()
		p := m.approvals[r.ApprovalID]
		if p == nil || p.user != r.Caller.User || p.tool != r.Tool || p.hash != hash ||
			p.summary != r.Summary || p.used {
			return "web", tool.Denied("invalid or expired approval")
		}
		switch p.decision {
		case "approved":
			p.used = true
			return "web", nil
		case "rejected":
			return "web", tool.Denied("rejected by human")
		default:
			return "web", m.pendingError(p)
		}
	}
	m.mu.Unlock()

	if supportsElicitation(req) {
		waitCtx, cancel := context.WithTimeout(ctx, m.ttl)
		result, err := req.Session.Elicit(waitCtx, &mcp.ElicitParams{
			Mode: "form",
			Message: fmt.Sprintf("opsgate approval request\n\n%s\n\nRequested by %s via %s. Approve?",
				r.Summary, r.Caller.User, r.Caller.Transport),
			RequestedSchema: map[string]any{
				"type": "object", "required": []string{"approve"},
				"properties": map[string]any{"approve": map[string]any{
					"type": "boolean", "title": "Approve this action",
				}},
			},
		})
		waitErr := waitCtx.Err()
		cancel()
		if err == nil && waitErr == nil {
			if result != nil && result.Action == "accept" && result.Content["approve"] == true {
				return "elicitation", nil
			}
			return "elicitation", tool.Denied("rejected by human")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if m.browser == nil {
		return "", tool.Denied("no human confirmation channel is available")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	// Bound all live records, including decided approvals awaiting consumption.
	if len(m.approvals) >= 100 {
		return "web", tool.Denied("too many pending approvals; try again after expiry")
	}
	id, err := randomToken(16)
	if err != nil {
		return "web", tool.Denied("cannot create approval")
	}
	csrf, err := randomToken(32)
	if err != nil || m.approvals[id] != nil {
		return "web", tool.Denied("cannot create approval")
	}
	p := &pending{
		id: id, csrf: csrf, user: r.Caller.User, transport: r.Caller.Transport,
		tool: r.Tool, summary: r.Summary, args: args, hash: hash,
		expires: m.now().Add(m.ttl),
	}
	m.approvals[id] = p
	return "web", m.pendingError(p)
}

func supportsElicitation(req *mcp.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return false
	}
	p := req.Session.InitializeParams()
	return p != nil && p.Capabilities != nil && p.Capabilities.Elicitation != nil
}

func (m *Manager) pendingError(p *pending) error {
	return fmt.Errorf("%w: ask the user to open %s/approve/%s, then call %s again with the same arguments plus approval_id %q. This approval expires at %s",
		tool.ErrApprovalPending, m.publicURL, p.id, p.tool, p.id, p.expires.UTC().Format(time.RFC3339Nano))
}

// Sweep during access so managers need no background goroutine or shutdown API.
func (m *Manager) sweepLocked() {
	now := m.now()
	for id, p := range m.approvals {
		if !now.Before(p.expires) {
			delete(m.approvals, id)
		}
	}
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func validID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

var _ tool.Confirmer = (*Manager)(nil)
