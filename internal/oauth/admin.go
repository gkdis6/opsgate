package oauth

import (
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/ui"
)

func (s *Server) adminRoutes(mux *http.ServeMux) {
	protect := http.NewCrossOriginProtection()
	protect.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ui.Message(w, http.StatusForbidden, "Request forbidden", "This request cannot be accepted from another site.")
	}))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmlHeaders(w)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; "+ui.ImageSource+"; frame-ancestors 'none'; form-action 'self'")
		protect.Handler(s.RequireUser(s.clientsPage)).ServeHTTP(w, r)
	})
	mux.Handle("GET /admin/clients", handler)
	mux.Handle("POST /admin/clients", handler)
}

func (s *Server) isAdmin(subject string) bool {
	for _, admin := range s.cfg.Admins {
		if subject == admin || !strings.HasPrefix(subject, "oidc:") && !strings.HasPrefix(admin, "oidc:") && strings.EqualFold(subject, admin) {
			return true
		}
	}
	return false
}

func (s *Server) clientsPage(w http.ResponseWriter, r *http.Request, subject string) {
	if !s.isAdmin(subject) {
		ui.Message(w, http.StatusForbidden, "Administrator access required", "This user cannot manage clients.")
		return
	}
	if r.Method == http.MethodPost {
		readDeadline(w)
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/x-www-form-urlencoded" || r.ParseForm() != nil || r.URL.RawQuery != "" {
			ui.Message(w, http.StatusBadRequest, "Invalid form", "The client management form could not be read.")
			return
		}
	}
	s.clientsResponse(r, subject)(w, r)
}

// Caller parses the body before acquiring mu. Recheck the session afterward.
func (s *Server) clientsResponse(r *http.Request, subject string) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if s.storageErr != nil || s.closed {
		return adminErrorResponse(http.StatusServiceUnavailable, "Client management unavailable")
	}
	cookie, err := r.Cookie(sessionCookie)
	var session *browserSession
	if err == nil {
		session = s.sessions[hash(cookie.Value)]
	}
	if session == nil || session.Subject != subject || !s.now().Before(session.Expires) {
		return adminErrorResponse(http.StatusForbidden, "Browser session expired")
	}
	if r.Method == http.MethodPost {
		csrf, id, action := r.PostForm["csrf"], r.PostForm["client_id"], r.PostForm["action"]
		if len(r.PostForm) != 3 || len(csrf) != 1 || len(id) != 1 || len(action) != 1 || session.CSRF == "" || !equal(csrf[0], session.CSRF) {
			return adminErrorResponse(http.StatusForbidden, "Invalid client management form")
		}
		c := s.clients[id[0]]
		if c == nil {
			return adminErrorResponse(http.StatusNotFound, "Client no longer registered")
		}
		switch action[0] {
		case "approve":
			if !c.Pending {
				return adminErrorResponse(http.StatusConflict, "Client already approved")
			}
			c.Pending = false
		case "reject", "delete":
			s.deleteClient(c.ID)
		default:
			return adminErrorResponse(http.StatusBadRequest, "Invalid client management action")
		}
		if s.save() != nil {
			return adminErrorResponse(http.StatusServiceUnavailable, "Client management unavailable")
		}
		session.CSRF = "" // A successful decision consumes this form's token.
		return func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/clients", http.StatusSeeOther)
		}
	}
	if session.CSRF == "" {
		session.CSRF = random()
	}
	view := clientsView{CSRF: session.CSRF}
	for _, c := range s.clients {
		row := clientView{ID: c.ID, Name: c.Name, Hosts: redirectHosts(c.RedirectURIs), Created: c.Created, LastUsed: c.LastUsed}
		if c.Pending {
			view.Pending = append(view.Pending, row)
		} else {
			view.Recent = append(view.Recent, row)
		}
	}
	newest := func(a, b clientView) int {
		if n := b.Created.Compare(a.Created); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	}
	slices.SortFunc(view.Pending, newest)
	slices.SortFunc(view.Recent, newest)
	if len(view.Recent) > 50 {
		view.Recent = view.Recent[:50]
	}
	return func(w http.ResponseWriter, _ *http.Request) { ui.Render(w, http.StatusOK, clientsPage, view) }
}

// Caller holds mu. Deletion revokes all credentials and in-flight logins.
func (s *Server) deleteClient(id string) {
	delete(s.clients, id)
	for key, t := range s.access {
		if t.ClientID == id {
			delete(s.access, key)
		}
	}
	for key, t := range s.refresh {
		if t.ClientID == id {
			delete(s.refresh, key)
		}
	}
	for key, c := range s.codes {
		if c.ClientID == id {
			delete(s.codes, key)
		}
	}
	for key, p := range s.pending {
		if p.ClientID == id {
			delete(s.pending, key)
		}
	}
}

type clientView struct {
	ID, Name          string
	Hosts             []string
	Created, LastUsed time.Time
}

type clientsView struct {
	CSRF            string
	Pending, Recent []clientView
}

var clientsPage = ui.NewTemplate(`{{define "title"}}Manage clients{{end}}
{{define "client"}}<article class="target"><p class="target-label">Redirect hosts</p>
{{range .Hosts}}<div class="host">{{.}}</div>{{end}}
<dl><dt>Client name (unverified, self-reported)</dt><dd>{{.Name}}</dd>
<dt>Client ID</dt><dd>{{.ID}}</dd><dt>Registered</dt><dd>{{.Created.UTC.Format "2006-01-02 15:04 UTC"}}</dd>
{{if not .LastUsed.IsZero}}<dt>Last used</dt><dd>{{.LastUsed.UTC.Format "2006-01-02 15:04 UTC"}}</dd>{{end}}</dl>{{end}}
{{define "content"}}<h1>Manage clients</h1><p>Review redirect hosts carefully before allowing a client to connect.</p>
<h2>Pending clients</h2>{{range .Pending}}{{template "client" .}}
<form method="post" action="/admin/clients"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="client_id" value="{{.ID}}">
<div class="actions"><button class="secondary" name="action" value="reject">Reject</button><button class="primary" name="action" value="approve">Approve</button></div></form></article>
{{else}}<p>No pending clients.</p>{{end}}
<h2>Recent clients</h2>{{range .Recent}}{{template "client" .}}
<form method="post" action="/admin/clients"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="client_id" value="{{.ID}}">
<button class="secondary" name="action" value="delete">Delete and revoke access</button></form></article>
{{else}}<p>No recent clients.</p>{{end}}{{end}}`)

func adminErrorResponse(status int, message string) response {
	return func(w http.ResponseWriter, _ *http.Request) {
		ui.Message(w, status, "Client management unavailable", message)
	}
}
