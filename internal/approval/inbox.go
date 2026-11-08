package approval

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/ui"
)

//go:embed inbox.js
var inboxScript string

//go:embed sw.js
var workerScript string

func (m *Manager) inboxRoutes(mux *http.ServeMux, protect *http.CrossOriginProtection) {
	authenticated := func(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.browser.RequireUser(next).ServeHTTP(w, r)
		})
	}
	wrap := func(h http.Handler) http.Handler {
		return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'; script-src 'self'; worker-src 'self'; manifest-src 'self'; connect-src 'self'")
			protect.Handler(h).ServeHTTP(w, r)
		}))
	}
	mux.Handle("GET /approvals", wrap(authenticated(m.inbox)))
	mux.Handle("GET /approvals/inbox.js", wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = io.WriteString(w, inboxScript)
	})))
	mux.Handle("GET /approvals/sw.js", wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Service-Worker-Allowed", "/approvals")
		body := "An approval is waiting"
		if m.push != nil && m.push.cfg.Language == "ko" {
			body = "승인 요청이 있습니다"
		}
		_, _ = io.WriteString(w, strings.ReplaceAll(workerScript, "NOTIFICATION_BODY", strconv.Quote(body)))
	})))
	mux.Handle("GET /approvals/icon.png", securityHeaders(http.HandlerFunc(ui.Icon)))
	mux.Handle("GET /approvals/manifest.webmanifest", wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = io.WriteString(w, `{"id":"/approvals","name":"opsgate","short_name":"opsgate","start_url":"/approvals","scope":"/","display":"standalone","icons":[{"src":"/approvals/icon.png","sizes":"512x512","type":"image/png","purpose":"any"}]}`)
	})))
	mux.Handle("GET /approvals/push/key", wrap(authenticated(func(w http.ResponseWriter, r *http.Request, user string) {
		if !m.pushAccess(w, user) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Key string `json:"key"`
		}{m.push.publicKey})
	})))
	for _, operation := range []string{"subscribe", "unsubscribe"} {
		mux.Handle("POST /approvals/push/"+operation, wrap(authenticated(m.subscription)))
	}
}

func (m *Manager) canSubscribe(user string) bool {
	access, ok := m.browser.(browserAccess)
	if !ok {
		return false
	}
	scopes, _, known := access.Access(user)
	return known && slices.Contains(scopes, config.ScopeWrite)
}

func (m *Manager) pushAccess(w http.ResponseWriter, user string) bool {
	if m.push == nil {
		http.Error(w, "Notifications are disabled", http.StatusNotFound)
		return false
	}
	if !m.canSubscribe(user) {
		http.Error(w, "This account cannot approve changes", http.StatusForbidden)
		return false
	}
	return true
}

// Bound to the signed-in user and this process's random key. A token copied
// from another user's inbox cannot change this user's subscriptions.
func (m *Manager) inboxCSRF(user string) string {
	if len(m.key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte("approval-push\x00" + user))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) subscription(w http.ResponseWriter, r *http.Request, user string) {
	if !m.pushAccess(w, user) {
		return
	}
	csrf := m.inboxCSRF(user)
	if csrf == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
		http.Error(w, "The notification request could not be verified", http.StatusForbidden)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "Expected JSON", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var sub pushSubscription
	if dec.Decode(&sub) != nil || dec.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid subscription", http.StatusBadRequest)
		return
	}
	if _, err := m.push.endpoint(sub.Endpoint); err != nil {
		http.Error(w, "Invalid or disallowed push endpoint", http.StatusBadRequest)
		return
	}
	if err := m.push.update(user, sub, strings.HasSuffix(r.URL.Path, "/unsubscribe")); err != nil {
		http.Error(w, "Cannot persist notification subscription", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type inboxItem struct {
	ID, Title, Target, Source, Status, Result string
	Created, Expires                          time.Time
	Pending                                   bool
}

type inboxView struct {
	CSRF  string
	Push  bool
	Items []inboxItem
}

func (m *Manager) inbox(w http.ResponseWriter, _ *http.Request, user string) {
	view := inboxView{CSRF: m.inboxCSRF(user), Push: m.push != nil && m.canSubscribe(user)}
	m.mu.Lock()
	m.sweepLocked()
	for _, p := range m.approvals {
		if p.user != user {
			continue
		}
		view.Items = append(view.Items, inboxItem{
			ID: p.id, Title: p.title, Target: p.target, Source: sourceLabel(p.source),
			Status: approvalStatus(p), Result: p.result, Created: p.created, Expires: p.expires, Pending: p.decision == "",
		})
	}
	m.mu.Unlock()
	slices.SortFunc(view.Items, func(a, b inboxItem) int {
		if a.Pending != b.Pending {
			if a.Pending {
				return -1
			}
			return 1
		}
		if cmp := b.Created.Compare(a.Created); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	ui.Render(w, http.StatusOK, inboxPage, view)
}

var inboxPage = ui.NewTemplate(`{{define "title"}}Approval inbox{{end}}
{{define "head"}}<link rel="manifest" href="/approvals/manifest.webmanifest"><link rel="apple-touch-icon" href="/approvals/icon.png">{{end}}
{{define "scripts"}}<script src="/approvals/inbox.js" defer></script>{{end}}
{{define "content"}}
<h1>Approval inbox</h1><p>Review a request to approve and run its action.</p>
{{if .Push}}<input type="hidden" id="push-csrf" name="csrf" value="{{.CSRF}}">
<button type="button" id="enable-push">Enable notifications on this device</button>
<button type="button" id="disable-push">Disable notifications on this device</button>
<p class="note">On iPhone, use Add to Home Screen first, then open opsgate from the Home Screen and enable notifications.</p>
<p id="push-status" role="status"></p>{{else}}<p class="note">Notifications are unavailable for this account or disabled on this server.</p>{{end}}
{{range .Items}}<article class="target"><h2><a href="/approve/{{.ID}}">{{.Title}}</a></h2>
{{if .Target}}<p>Target: {{.Target}}</p>{{end}}
<dl><dt>Status</dt><dd>{{.Status}}</dd><dt>Requested</dt><dd>{{.Created.UTC.Format "2006-01-02 15:04:05 UTC"}}</dd><dt>Expires</dt><dd>{{.Expires.UTC.Format "2006-01-02 15:04:05 UTC"}}</dd>
{{if .Source}}<dt>Suggested by</dt><dd>{{.Source}}</dd>{{end}}</dl>
{{if .Result}}<pre>{{.Result}}</pre>{{end}}</article>{{else}}<p>No live approvals.</p>{{end}}
{{end}}`)
