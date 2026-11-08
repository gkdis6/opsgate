package approval

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
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
// Invalid or masked values produce no link.
func RestartURL(base, cluster, kind, namespace, name string) string {
	link, _ := action.URL(base, restartTool, map[string]any{"cluster": cluster, "kind": kind, "namespace": namespace, "name": name}, "rule")
	return link
}

func (m *Manager) restartRequest(w http.ResponseWriter, r *http.Request, subject string) {
	m.actionRequest(w, r, subject)
}

func (m *Manager) restoreImageRequest(w http.ResponseWriter, r *http.Request, subject string) {
	m.actionRequest(w, r, subject)
}

func (m *Manager) actionRequest(w http.ResponseWriter, r *http.Request, subject string) {
	entry, values, source, err := action.Parse(r.URL)
	if err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid action", err.Error())
		return
	}
	m.directRequest(w, r, subject, entry.Tool, values, source)
}

func (m *Manager) directRequest(w http.ResponseWriter, r *http.Request, subject, name string, values map[string]any, source string) {
	var d *tool.Direct
	if m.direct != nil {
		d = m.direct(name)
	}
	access, canCheck := m.browser.(browserAccess)
	if d == nil || !canCheck {
		ui.Message(w, http.StatusNotFound, "Action unavailable", "This server does not offer this action from a link.")
		return
	}
	scopes, tools, ok := access.Access(subject)
	if !ok {
		ui.Message(w, http.StatusForbidden, "Approval forbidden", "This account cannot approve changes.")
		return
	}
	raw, err := json.Marshal(values)
	if err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid link", "This action link could not be read.")
		return
	}
	caller := tool.Caller{User: subject, Transport: "web", Scopes: scopes, Tools: tools, Source: source}
	plan, summary, err := d.Plan(r.Context(), caller, raw)
	if err != nil {
		message := redact.String(err.Error())
		if name == "k8s_restore_image" {
			message += " 이 승인 링크는 더 이상 유효하지 않을 수 있습니다. 대상 삭제 또는 허용 정책 변경 여부를 확인하고 새 분석 결과의 링크를 사용하세요."
		}
		ui.Message(w, http.StatusForbidden, "Action unavailable", message)
		return
	}
	id, idErr := randomToken(16)
	csrf, csrfErr := randomToken(32)
	if idErr != nil || csrfErr != nil {
		ui.Message(w, http.StatusServiceUnavailable, "Action unavailable", "The approval could not be created.")
		return
	}
	display, err := json.Marshal(redact.Value(values))
	if err != nil {
		ui.Message(w, http.StatusBadRequest, "Invalid link", "This action link could not be read.")
		return
	}
	m.mu.Lock()
	m.sweepLocked()
	// Bound all live records, including running and retained outcomes.
	if len(m.approvals) >= 100 || m.approvals[id] != nil {
		m.mu.Unlock()
		ui.Message(w, http.StatusServiceUnavailable, "Action unavailable", "Too many approvals are waiting. Try again after they expire.")
		return
	}
	m.approvals[id] = &pending{
		id: id, csrf: csrf, user: subject, transport: caller.Transport, tool: name, title: label(name), source: source, summary: summary,
		target: plan.Target, noWindow: true, args: display, created: m.now(), expires: m.now().Add(m.ttl),
		run: func(ctx context.Context) (string, error) { return d.Run(ctx, caller, raw, summary) },
	}
	m.mu.Unlock()
	m.audit.Record(audit.Event{Time: m.now(), User: redact.String(subject), Transport: caller.Transport, Tool: name, Outcome: "pending",
		Approval: "web", Source: source, Plan: summary, Args: map[string]any{"approval_id_prefix": id[:8]}})
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
	p.failed = err != nil
	p.finished = true
	p.run = nil
	p.retainUntil = m.now().Add(10 * time.Minute)
	if p.done != nil {
		close(p.done)
	}
	m.mu.Unlock()
	return result
}

func label(name string) string {
	entry, _ := action.Lookup(name)
	return entry.Label("en")
}
