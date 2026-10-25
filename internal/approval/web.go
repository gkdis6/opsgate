package approval

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"net/http"
	"time"
)

// Routes registers the /approve/ pages when web approval is enabled.
func (m *Manager) Routes(mux *http.ServeMux) {
	if m.browser == nil {
		return
	}
	protect := http.NewCrossOriginProtection()
	for _, method := range []string{"GET", "POST"} {
		h := m.browser.RequireUser(m.page)
		mux.Handle(method+" /approve/{id}", securityHeaders(protect.Handler(h)))
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) page(w http.ResponseWriter, r *http.Request, subject string) {
	if !validID(r.PathValue("id")) {
		http.NotFound(w, r)
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
			http.Error(w, "invalid form", http.StatusBadRequest)
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
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if decision[0] != "approve" && decision[0] != "reject" {
			m.mu.Unlock()
			http.Error(w, "invalid decision", http.StatusBadRequest)
			return
		}
		if p.decision != "" || p.used {
			m.mu.Unlock()
			http.Error(w, "approval already decided", http.StatusConflict)
			return
		}
		p.decision = "rejected"
		if decision[0] == "approve" {
			p.decision = "approved"
		}
	}
	view := struct {
		Tool, Summary, Args, User, Transport, Expires, CSRF, Decision string
		Used                                                          bool
	}{
		Tool: p.tool, Summary: p.summary, User: p.user, Transport: p.transport,
		Expires: p.expires.UTC().Format(time.RFC3339Nano), CSRF: p.csrf,
		Decision: p.decision, Used: p.used,
	}
	argsJSON := p.args
	m.mu.Unlock()
	var args bytes.Buffer
	_ = json.Indent(&args, argsJSON, "", "  ")
	view.Args = args.String()

	var body bytes.Buffer
	if err := approvalPage.Execute(&body, view); err != nil {
		http.Error(w, "cannot render approval", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body.Bytes())
}

// lockApproval returns an owned, live approval with m.mu held on success.
func (m *Manager) lockApproval(w http.ResponseWriter, r *http.Request, subject string) *pending {
	m.mu.Lock()
	m.sweepLocked()
	p := m.approvals[r.PathValue("id")]
	if p == nil {
		m.mu.Unlock()
		http.NotFound(w, r)
		return nil
	}
	if subject != p.user {
		m.mu.Unlock()
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	return p
}

var approvalPage = template.Must(template.New("approval").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>opsgate approval</title></head>
<body><h1>Approve an opsgate action</h1>
<dl><dt>Tool</dt><dd>{{.Tool}}</dd><dt>Summary</dt><dd><pre>{{.Summary}}</pre></dd>
<dt>Arguments</dt><dd><pre>{{.Args}}</pre></dd>
<dt>Requester</dt><dd>{{.User}} via {{.Transport}}</dd><dt>Expires</dt><dd>{{.Expires}}</dd></dl>
{{if .Used}}<p>Approval already used.</p>
{{else if .Decision}}<p>Decision: {{.Decision}}. Return to your client to continue.</p>
{{else}}<form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit" name="decision" value="approve">Approve</button>
<button type="submit" name="decision" value="reject">Reject</button></form>{{end}}
</body></html>`))
