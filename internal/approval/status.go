package approval

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/tool"
)

type statusInput struct {
	ApprovalID string `json:"approval_id" jsonschema:"ID returned with a web approval link"`
}

// RegisterStatus exposes only the requester's approval records. Add supplies
// scope, per-caller tool policy, timeout, audit and output bounds.
func (m *Manager) RegisterStatus(s *mcp.Server, env *tool.Env) {
	if m.browser == nil {
		return
	}
	tool.Add(s, env, tool.Spec{Name: "approval_status", Title: "Approval status", ReadOnly: true,
		Description: "Read your web approval's status and execution result using approval_id."},
		func(_ context.Context, c tool.Caller, in statusInput) (string, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.sweepLocked()
			p := m.approvals[in.ApprovalID]
			if !validID(in.ApprovalID) || p == nil || p.user != c.User {
				return "", tool.Denied("unknown or expired approval")
			}
			status := approvalStatus(p)
			text, _, undo := m.splitUndo(p.result)
			result, err := json.Marshal(struct {
				Tool    string `json:"tool"`
				Target  string `json:"target"`
				Status  string `json:"status"`
				Result  string `json:"result,omitempty"`
				UndoURL string `json:"undo_url,omitempty"`
			}{p.tool, p.target, status, text, undo})
			return string(result), err
		})
}

func approvalStatus(p *pending) string {
	switch {
	case p.decision == "rejected":
		return "rejected"
	case p.finished && p.failed:
		return "not_applied"
	case p.finished:
		return "applied"
	case p.decision == "approved":
		return "running"
	default:
		return "pending"
	}
}
