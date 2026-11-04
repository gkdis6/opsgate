// Package approval obtains human confirmation for write tools, either via
// MCP elicitation inside the client or via a browser approval page.
package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
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
	mu          sync.Mutex
	audit       *audit.Logger
	requireWeb  bool
	ttl         time.Duration
	publicURL   string
	browser     BrowserAuth
	approvals   map[string]*pending
	key         []byte
	used        map[string]time.Time
	now         func() time.Time
	window      time.Duration
	windowTools []string
	grants      map[grantKey]time.Time
	direct      func(name string) *tool.Direct
}

const splitWriteMessage = "change is too large to review in the client; split it into smaller writes"

type pending struct {
	id, csrf, user, transport, tool, title, summary string
	target                                          string
	noWindow                                        bool
	fullReviewRequired                              bool
	args                                            []byte
	hash                                            [sha256.Size]byte
	expires                                         time.Time
	decision                                        string
	used                                            bool
	window                                          time.Duration
	// run applies a direct write once approved; no MCP client can claim it.
	run    func(context.Context) (string, error)
	result string
}

// New returns a Manager. browser may be nil, which disables web approval.
// publicURL is the origin used to build approval links.
func New(cfg config.Approval, publicURL string, browser BrowserAuth, logger *audit.Logger) *Manager {
	m := &Manager{
		audit: logger, requireWeb: cfg.RequireWeb, ttl: cfg.TTL, publicURL: strings.TrimRight(publicURL, "/"), browser: browser,
		approvals: make(map[string]*pending), used: make(map[string]time.Time), now: time.Now,
		window: cfg.Window, windowTools: slices.Clone(cfg.WindowTools), grants: make(map[grantKey]time.Time),
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err == nil {
		m.key = key
	}
	return m
}

// Confirm implements tool.Confirmer.
func (m *Manager) Confirm(ctx context.Context, req *mcp.CallToolRequest, r tool.ConfirmRequest) (method string, err error) {
	// Audit all denied multi round-trip verifications, including malformed input.
	defer func() {
		if err != nil && errors.Is(err, tool.ErrDenied) && req != nil && req.Params != nil &&
			(req.Params.RequestState != "" || len(req.Params.InputResponses) != 0) {
			m.audit.Record(audit.Event{Time: m.now(), User: redact.String(r.Caller.User), Transport: r.Caller.Transport,
				Tool: r.Tool, Outcome: "denied", Approval: "elicitation", Error: redact.String(err.Error())})
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	maxArgs := r.MaxArgBytes
	if maxArgs <= 0 {
		maxArgs = 16 << 10
	}
	if m.ttl <= 0 || r.Caller.User == "" || len(r.Caller.User) > 1024 ||
		len(r.Caller.Transport) > 64 || r.Tool == "" || len(r.Tool) > 256 || r.Summary == "" || len(r.Summary) > r.SummaryLimit() ||
		len(r.Target) > 256 || !utf8.ValidString(r.Target) || strings.IndexFunc(r.Target, unicode.IsControl) >= 0 {
		return "", tool.Denied("invalid approval request or TTL")
	}
	if r.ApprovalID != "" && !validID(r.ApprovalID) {
		return "web", tool.Denied("invalid or expired approval")
	}
	args, err := json.Marshal(redact.Value(r.Args))
	if err != nil || len(args) > maxArgs {
		return "", tool.Denied("approval arguments must be JSON of at most %d KiB", maxArgs>>10)
	}
	if redact.String(r.Summary) != r.Summary || redact.String(r.Target) != r.Target {
		return "", tool.Denied("target cannot be shown unambiguously")
	}
	if m.requireWeb && r.Caller.Transport != "http" {
		if r.FullReviewRequired {
			return "", tool.Denied(splitWriteMessage)
		}
		return "", tool.Denied("browser approval is required")
	}
	m.mu.Lock()
	m.sweepLocked()
	granted := m.windowEligible(r.Tool, r.NoWindow) && m.now().Before(m.grants[grantKey{r.Caller.User, r.Tool, r.Target}])
	m.mu.Unlock()
	if granted {
		return "window", nil
	}
	if r.FullReviewRequired && (r.Caller.Transport != "http" || m.browser == nil) {
		return "", tool.Denied(splitWriteMessage)
	}
	hash := r.ArgsHash
	if req != nil && req.Params != nil {
		if (m.requireWeb || r.FullReviewRequired) && (req.Params.RequestState != "" || len(req.Params.InputResponses) != 0) {
			return "elicitation", tool.Denied("browser approval is required")
		}
		if response, ok := req.Params.InputResponses[approvalInputID]; ok {
			return m.confirmInput(req.Params.RequestState, response, r, hash)
		}
		if req.Params.RequestState != "" || len(req.Params.InputResponses) != 0 {
			return "elicitation", tool.Denied("invalid or expired approval")
		}
	}
	m.mu.Lock()
	m.sweepLocked()
	if r.ApprovalID != "" {
		defer m.mu.Unlock()
		p := m.approvals[r.ApprovalID]
		if p == nil || p.run != nil || p.user != r.Caller.User || p.transport != r.Caller.Transport || p.tool != r.Tool || p.hash != hash ||
			p.summary != r.Summary || p.target != r.Target || p.noWindow != r.NoWindow || p.fullReviewRequired != r.FullReviewRequired || p.used {
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

	if !m.requireWeb && !r.FullReviewRequired && supportsElicitation(req) {
		return "elicitation", m.requestInput(r, hash)
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
		tool: r.Tool, title: displayTitle(r), summary: r.Summary, args: args, hash: hash,
		target: r.Target, noWindow: r.NoWindow,
		fullReviewRequired: r.FullReviewRequired,
		expires:            m.now().Add(m.ttl),
	}
	m.approvals[id] = p
	return "web", m.pendingError(p)
}

func supportsElicitation(req *mcp.CallToolRequest) bool {
	if req == nil {
		return false
	}
	caps := req.ClientCapabilities()
	return caps != nil && caps.Elicitation != nil
}

func (m *Manager) pendingError(p *pending) error {
	target := ""
	if p.target != "" {
		target = " for " + p.target
	}
	return fmt.Errorf("%w%s: ask the user to open %s/approve/%s, then call %s again with the same arguments plus approval_id %q. This approval expires at %s",
		tool.ErrApprovalPending, target, m.publicURL, p.id, p.tool, p.id, p.expires.UTC().Format(time.RFC3339Nano))
}

// Sweep during access so managers need no background goroutine or shutdown API.
func (m *Manager) sweepLocked() {
	now := m.now()
	for id, p := range m.approvals {
		if !now.Before(p.expires) {
			delete(m.approvals, id)
		}
	}
	for nonce, expires := range m.used {
		if !now.Before(expires) {
			delete(m.used, nonce)
		}
	}
	for key, expires := range m.grants {
		if !now.Before(expires) {
			delete(m.grants, key)
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

// displayTitle names the tool for people: its title when it has one (titles
// are fixed by the server, not by the client), else its name.
func displayTitle(r tool.ConfirmRequest) string {
	if t := strings.TrimSpace(r.Title); t != "" && len(t) <= 128 {
		return t
	}
	return r.Tool
}
