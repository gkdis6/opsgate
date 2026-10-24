// Package approval obtains human confirmation for write tools, either via
// MCP elicitation inside the client or via a browser approval page.
package approval

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// BrowserAuth signs a person in for the approval page. Implemented by
// oauth.Server.
type BrowserAuth interface {
	RequireUser(next func(w http.ResponseWriter, r *http.Request, subject string)) http.Handler
}

// Manager implements tool.Confirmer.
type Manager struct {
	// TODO(codex): implement.
}

// New returns a Manager. browser may be nil, which disables web approval.
// publicURL is the origin used to build approval links.
func New(cfg config.Approval, publicURL string, browser BrowserAuth) *Manager {
	// TODO(codex): implement.
	return &Manager{}
}

// Confirm implements tool.Confirmer.
func (m *Manager) Confirm(ctx context.Context, req *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	// TODO(codex): implement.
	return "", tool.Denied("approval not implemented")
}

// Routes registers the /approve/ pages when web approval is enabled.
func (m *Manager) Routes(mux *http.ServeMux) {
	// TODO(codex): implement.
}

var _ tool.Confirmer = (*Manager)(nil)
