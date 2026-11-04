package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/gkdis6/opsgate/internal/ui"
)

const (
	notApplied     = "Not applied: "
	restartTool    = "k8s_rollout_restart"
	restartPath    = "/approve/restart/"
	restartPattern = restartPath + "{cluster}/{kind}/{namespace}/{name}"
)

// browserAccess is implemented by a BrowserAuth that knows what its users may
// do. Without it a browser sign-in cannot run a write by itself.
type browserAccess interface {
	Access(subject string) (scopes, tools []string, ok bool)
}

// SetDirect supplies the writes that can run from an approval page.
func (m *Manager) SetDirect(lookup func(name string) *tool.Direct) { m.direct = lookup }

// RestartURL returns the link that requests approval to restart a workload.
// It holds no secret and changes nothing until a signed-in person approves.
func RestartURL(base, cluster, kind, namespace, name string) string {
	return strings.TrimRight(base, "/") + restartPath + url.PathEscape(cluster) + "/" + url.PathEscape(kind) + "/" + url.PathEscape(namespace) + "/" + url.PathEscape(name)
}

// restartRequest shows the signed-in person a fresh plan to approve. Opening
// the link changes nothing but one expiring approval record.
func (m *Manager) restartRequest(w http.ResponseWriter, r *http.Request, subject string) {
	var d *tool.Direct
	if m.direct != nil {
		d = m.direct(restartTool)
	}
	access, canCheck := m.browser.(browserAccess)
	if d == nil || !canCheck {
		ui.Message(w, http.StatusNotFound, "Restart unavailable", "This server does not offer restarts from a link.")
		return
	}
	scopes, tools, ok := access.Access(subject)
	if !ok {
		ui.Message(w, http.StatusForbidden, "Approval forbidden", "This account cannot approve changes.")
		return
	}
	values := map[string]any{"cluster": r.PathValue("cluster"), "kind": r.PathValue("kind"), "namespace": r.PathValue("namespace"), "name": r.PathValue("name")}
	raw, err := json.Marshal(values)
	if err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid link", "This restart link could not be read.")
		return
	}
	caller := tool.Caller{User: subject, Transport: "web", Scopes: scopes, Tools: tools}
	plan, summary, err := d.Plan(r.Context(), caller, raw)
	if err != nil {
		ui.Message(w, http.StatusForbidden, "Restart unavailable", redact.String(err.Error()))
		return
	}
	id, idErr := randomToken(16)
	csrf, csrfErr := randomToken(32)
	if idErr != nil || csrfErr != nil {
		ui.Message(w, http.StatusServiceUnavailable, "Restart unavailable", "The approval could not be created.")
		return
	}
	display, err := json.Marshal(redact.Value(values))
	if err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid link", "This restart link could not be read.")
		return
	}
	m.mu.Lock()
	m.sweepLocked()
	// Bound all live records, including decided approvals awaiting consumption.
	if len(m.approvals) >= 100 || m.approvals[id] != nil {
		m.mu.Unlock()
		ui.Message(w, http.StatusServiceUnavailable, "Restart unavailable", "Too many approvals are waiting. Try again after they expire.")
		return
	}
	m.approvals[id] = &pending{
		id: id, csrf: csrf, user: subject, transport: caller.Transport, tool: restartTool, title: d.Title, summary: summary,
		target: plan.Target, noWindow: true, args: display, expires: m.now().Add(m.ttl),
		run: func(ctx context.Context) (string, error) { return d.Run(ctx, caller, raw, summary) },
	}
	m.mu.Unlock()
	m.audit.Record(audit.Event{Time: m.now(), User: redact.String(subject), Transport: caller.Transport, Tool: restartTool, Outcome: "pending",
		Approval: "web", Plan: summary, Args: map[string]any{"approval_id_prefix": id[:8]}})
	http.Redirect(w, r, "/approve/"+id, http.StatusSeeOther)
}

// runDirect applies an approved write. The person leaving the page must not
// cancel a write they approved, so only the tool's own deadline applies.
func (m *Manager) runDirect(ctx context.Context, p *pending, run func(context.Context) (string, error)) string {
	result, err := run(context.WithoutCancel(ctx))
	if err != nil {
		result = notApplied + redact.String(err.Error())
	}
	m.mu.Lock()
	p.result = result
	m.mu.Unlock()
	return result
}
