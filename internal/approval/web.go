package approval

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/ui"
)

// Routes registers the /approve/ pages when web approval is enabled.
func (m *Manager) Routes(mux *http.ServeMux) {
	if m.browser == nil {
		return
	}
	protect := http.NewCrossOriginProtection()
	protect.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ui.Message(w, http.StatusForbidden, "Approval forbidden", "This request cannot be accepted from another site.")
	}))
	for _, method := range []string{"GET", "POST"} {
		mux.Handle(method+" /approve/{id}", securityHeaders(protect.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.mu.Lock()
			m.sweepLocked()
			exists := validID(r.PathValue("id")) && m.approvals[r.PathValue("id")] != nil
			m.mu.Unlock()
			if !exists {
				unavailablePage(w)
				return
			}
			m.browser.RequireUser(m.page).ServeHTTP(w, r)
		}))))
	}
	mux.Handle("GET "+restartPattern, securityHeaders(protect.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.browser.RequireUser(m.restartRequest).ServeHTTP(w, r)
	}))))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; "+ui.ImageSource+"; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) page(w http.ResponseWriter, r *http.Request, subject string) {
	var decisionEvent, grantEvent *audit.Event
	if !validID(r.PathValue("id")) {
		unavailablePage(w)
		return
	}
	p := m.lockApproval(w, r, subject)
	if p == nil {
		return
	}
	if r.Method == http.MethodPost {
		// A slow body must not hold the manager lock. Recheck expiry afterward.
		m.mu.Unlock()
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			ui.Message(w, http.StatusBadRequest, "Invalid form", "The approval form could not be read.")
			return
		}
		p = m.lockApproval(w, r, subject)
		if p == nil {
			return
		}
		csrf, decision := r.PostForm["csrf"], r.PostForm["decision"]
		if len(r.PostForm) != 2 || len(csrf) != 1 || len(decision) != 1 ||
			subtle.ConstantTimeCompare([]byte(csrf[0]), []byte(p.csrf)) != 1 {
			m.mu.Unlock()
			ui.Message(w, http.StatusForbidden, "Approval forbidden", "The approval form could not be verified.")
			return
		}
		if decision[0] != "approve" && decision[0] != "reject" &&
			(decision[0] != "approve_window" || !m.windowEligible(p.tool, p.noWindow)) {
			m.mu.Unlock()
			ui.Message(w, http.StatusBadRequest, "Invalid decision", "Choose one of the available approval options.")
			return
		}
		if p.decision != "" || p.used {
			m.mu.Unlock()
			ui.Message(w, http.StatusConflict, "Approval already decided", "This approval already has a decision. Return to your chat.")
			return
		}
		if decision[0] == "approve_window" {
			var err error
			grantEvent, err = m.grantLocked(grantKey{p.user, p.tool, p.target}, p.transport, "web", p.noWindow)
			if err != nil {
				m.mu.Unlock()
				ui.Message(w, http.StatusServiceUnavailable, "Approval window unavailable", "Too many approval windows are active. Approve this call only, or try again after expiry.")
				return
			}
			p.window = m.window
		}
		p.decision = "approved"
		if decision[0] == "reject" {
			p.decision = "rejected"
		}
		decisionEvent = &audit.Event{Time: m.now(), User: redact.String(p.user), Transport: p.transport,
			Tool: p.tool, Outcome: p.decision, Approval: "web", Args: map[string]any{"approval_id_prefix": p.id[:8]}}
	}
	view := approvalView{
		PageTitle: "Approve: " + p.title, Tool: p.tool, Lines: planLines(p.summary), User: p.user, Transport: p.transport,
		Target: p.target, NoWindow: p.noWindow,
		Expires: fmt.Sprintf("Expires in %d min (%s UTC)", int(math.Ceil(p.expires.Sub(m.now()).Minutes())), p.expires.UTC().Format("15:04")),
		CSRF:    p.csrf, Used: p.used, FullReview: p.fullReviewRequired,
	}
	if m.windowEligible(p.tool, p.noWindow) {
		view.Window = windowLabel(m.window)
	}
	if p.decision != "" {
		view.Status = "Approved"
		if p.decision == "rejected" {
			view.Status = "Rejected"
		} else if p.window != 0 {
			view.Status = "Allowed for " + windowLabel(p.window)
		}
		view.PageTitle = view.Status
	}
	argsJSON := p.args
	var run func(context.Context) (string, error)
	if decisionEvent != nil && p.decision == "approved" && p.run != nil {
		run = p.run
	}
	view.Direct = p.run != nil
	view.Result = p.result
	m.mu.Unlock()
	if grantEvent != nil {
		m.audit.Record(*grantEvent)
	}
	if decisionEvent != nil {
		m.audit.Record(*decisionEvent)
	}
	if run != nil {
		view.Result = m.runDirect(r.Context(), p, run)
	}
	if view.Direct && strings.HasPrefix(view.Result, notApplied) {
		view.Status, view.PageTitle = "Not applied", "Not applied"
	}
	var args bytes.Buffer
	_ = json.Indent(&args, argsJSON, "", "  ")
	view.Args = args.String()
	ui.Render(w, http.StatusOK, approvalPage, view)
}

// lockApproval returns an owned, live approval with m.mu held on success.
func (m *Manager) lockApproval(w http.ResponseWriter, r *http.Request, subject string) *pending {
	m.mu.Lock()
	m.sweepLocked()
	p := m.approvals[r.PathValue("id")]
	if p == nil {
		m.mu.Unlock()
		unavailablePage(w)
		return nil
	}
	if subject != p.user {
		m.mu.Unlock()
		ui.Message(w, http.StatusForbidden, "Approval forbidden", "Sign in as the user who requested this approval.")
		return nil
	}
	return p
}

func unavailablePage(w http.ResponseWriter) {
	ui.Message(w, http.StatusNotFound, "Approval unavailable", "This approval is unknown or has expired. Ask your assistant to request a new approval.")
}

type approvalView struct {
	PageTitle, Tool, Args, User, Transport, Expires, CSRF, Status, Window string
	// Direct means approving runs the write here, so Result replaces the chat.
	Direct bool
	Result string
	// FullReview opens the arguments because the summary omits content.
	FullReview     bool
	Target         string
	Lines          []planLine
	Used, NoWindow bool
}

type planLine struct{ Text, Class string }

func planLines(summary string) []planLine {
	lines := strings.Split(summary, "\n")
	out := make([]planLine, 0, len(lines))
	for _, line := range lines {
		class := ""
		switch {
		case strings.HasPrefix(line, "+"):
			class = "added"
		case strings.HasPrefix(line, "-"):
			class = "removed"
		case strings.HasPrefix(line, "@@"):
			class = "hunk"
		}
		out = append(out, planLine{line, class})
	}
	return out
}

var approvalPage = ui.NewTemplate(`{{define "title"}}{{.PageTitle}}{{end}}
{{define "content"}}
{{if .Status}}<h1 class="status">{{.Status}}</h1><p>Tool: <strong>{{.Tool}}</strong></p>
{{else}}<h1>{{.PageTitle}}</h1><p>Review the target and planned changes before approving.</p>{{end}}
{{if .Target}}<div class="target"><strong>Target: {{.Target}}</strong></div>{{end}}
{{if .NoWindow}}<p class="note">This target requires approval for every call.</p>{{end}}
<div class="target plan" aria-label="Target and plan">{{range .Lines}}<span class="plan-line {{.Class}}">{{if .Text}}{{.Text}}{{else}} {{end}}</span>{{end}}</div>
{{if .Status}}
{{if .Direct}}{{if .Result}}<p>{{.Result}}</p>{{end}}{{else}}
{{if .Used}}<p class="muted">Approval already used.</p>{{end}}
<p>Return to your chat — the assistant will continue when you ask it to retry.</p>{{end}}
{{else}}
<dl><dt>Requester</dt><dd>{{.User}} via {{.Transport}}</dd><dt>Expires</dt><dd>{{.Expires}}</dd></dl>
<details{{if .FullReview}} open{{end}}><summary>Arguments</summary><pre>{{.Args}}</pre></details>
<form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><div class="actions">
<button class="secondary" type="submit" name="decision" value="reject">Reject</button>
<button class="primary" type="submit" name="decision" value="approve">Approve</button>
{{if .Window}}<button class="primary outline" type="submit" name="decision" value="approve_window">Approve + allow for {{.Window}}</button>{{end}}
</div>{{if .Window}}<p class="note">The window allows {{.Tool}}{{if .Target}} on {{.Target}}{{end}} with any valid arguments for your user for the next {{.Window}}.</p>{{end}}</form>
{{end}}{{end}}`)
