package approval

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestApprovalPagesEscaping(t *testing.T) {
	for _, page := range []string{"pending", "approved", "rejected", "window", "unknown", "expired", "forbidden"} {
		t.Run(page, func(t *testing.T) {
			m, mux := windowManager(windowConfig(), nil)
			r := approvalRequest()
			r.Summary = "Change <script>summary</script>\n+<img src=x>\n-removed\n@@ hunk\n context"
			r.Args["extra"] = "<script>args</script>"
			id := pendingID(t, m, r)
			user := r.Caller.User
			method := http.MethodGet
			var form url.Values
			switch page {
			case "approved", "rejected", "window":
				decision := map[string]string{"approved": "approve", "rejected": "reject", "window": "approve_window"}[page]
				form = url.Values{"csrf": {readCSRF(t, mux, id)}, "decision": {decision}}
				method = http.MethodPost
			case "unknown":
				id = strings.Repeat("a", 26)
			case "expired":
				m.now = func() time.Time { return time.Now().Add(time.Hour) }
			case "forbidden":
				user = "github:other"
			}
			w := webRequest(mux, method, id, user, form, nil)
			body := w.Body.String()
			if strings.Contains(strings.ToLower(body), "<script") || !strings.Contains(body, `name="viewport"`) || !strings.Contains(body, `class="brand"><img`) {
				t.Fatalf("unsafe or unstyled page: %s", body)
			}
			wantStatus := http.StatusOK
			if page == "unknown" || page == "expired" {
				wantStatus = http.StatusNotFound
			} else if page == "forbidden" {
				wantStatus = http.StatusForbidden
			}
			if w.Code != wantStatus {
				t.Fatalf("status %d: %s", w.Code, body)
			}
			if wantStatus != http.StatusOK {
				if strings.Contains(body, "&lt;script&gt;summary") || strings.Contains(body, "args&lt;/script") {
					t.Fatal("error page leaked approval")
				}
				return
			}
			for _, want := range []string{"&lt;script&gt;summary&lt;/script&gt;", "&lt;img src=x&gt;", `class="plan-line added"`, `class="plan-line removed"`, `class="plan-line hunk"`} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q: %s", want, body)
				}
			}
			if page == "pending" {
				for _, want := range []string{"Approve: restart", `\u003cscript\u003eargs\u003c/script\u003e`, "<details>", "Expires in 1 min (", "Approve + allow for 30m"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %q", want)
					}
				}
			} else {
				status := map[string]string{"approved": "Approved", "rejected": "Rejected", "window": "Allowed for 30m"}[page]
				if !strings.Contains(body, `class="status">`+status) || !strings.Contains(body, "Return to your chat — the assistant will continue when you ask it to retry.") || strings.Contains(body, "<form") || w.Header().Get("Location") != "" {
					t.Fatalf("bad result page: %s", body)
				}
			}
		})
	}
}

func TestApprovalPageUsesToolTitle(t *testing.T) {
	m, mux := windowManager(windowConfig(), nil)
	r := approvalRequest()
	r.Title = "Restart <service>"
	id := pendingID(t, m, r)
	body := webRequest(mux, http.MethodGet, id, r.Caller.User, nil, nil).Body.String()
	if !strings.Contains(body, "Approve: Restart &lt;service&gt;") {
		t.Fatalf("title not shown or not escaped: %s", body)
	}
}
