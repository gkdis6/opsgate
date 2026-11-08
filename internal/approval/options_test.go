package approval

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestChoicesPageAndTrust(t *testing.T) {
	const target = "/approve/options/main/apps/web-abc?workload=deployment%2Fweb"
	for _, scenario := range []string{"choices", "gone", "anonymous", "read only", "write only", "unknown", "tool denied", "action denied", "malformed", "duplicate", "unsupported hint", "post", "cross origin post", "cross origin navigation"} {
		t.Run(scenario, func(t *testing.T) {
			access := writer()
			switch scenario {
			case "read only":
				access.scopes = []string{config.ScopeRead}
			case "write only":
				access.scopes = []string{config.ScopeWrite}
			case "unknown":
				access.known = false
			case "tool denied":
				access.tools = []string{"note_*"}
			case "action denied":
				access.tools = []string{"k8s_remediation_options"}
			}
			f := newRestartFixture(t, access)
			calls := 0
			f.manager.SetOptions(func(_ context.Context, c tool.Caller, cluster, ns, pod, workload string) (action.Options, error) {
				calls++
				if c.User != "alice" || cluster != "main" || ns != "apps" || pod != "web-abc" || workload != "deployment/web" {
					t.Fatal("caller/target binding lost")
				}
				return action.Options{PodGone: scenario == "gone", Hint: "Check events", Actions: []action.Choice{{URL: "https://ops.example" + genericRestart + "&source=rule", Reason: "Fixed reason <script>alert(1)</script>", ReasonKO: "고정 이유"}}}, nil
			})
			path, method, user := target, "GET", "alice"
			switch scenario {
			case "anonymous":
				user = ""
			case "malformed":
				path += "&next=https://evil.example"
			case "duplicate":
				path += "&workload=deployment/web"
			case "unsupported hint":
				path = "/approve/options/main/apps/web-abc?workload=job/web"
			case "post", "cross origin post":
				method = "POST"
			}
			r := httptest.NewRequest(method, path, nil)
			r.Header.Set("X-Test-User", user)
			if strings.HasPrefix(scenario, "cross origin") {
				r.Header.Set("Origin", "https://evil.example")
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			f.mux.ServeHTTP(w, r)
			switch scenario {
			case "choices", "gone", "cross origin navigation":
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Source / 출처: rule") || !strings.Contains(w.Body.String(), "/approve/action/") || strings.Contains(w.Body.String(), "<script>") {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				if scenario == "gone" && !strings.Contains(w.Body.String(), "pod is gone") {
					t.Fatal(w.Body)
				}
				if len(f.manager.approvals) != 0 {
					t.Fatal("choices created an approval")
				}
			case "action denied":
				if w.Code != 200 || strings.Contains(w.Body.String(), "/approve/action/") {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
			default:
				if w.Code == 200 || calls != 0 {
					t.Fatalf("unsafe request accepted: %d, calls=%d", w.Code, calls)
				}
			}
			for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "Cache-Control", "Referrer-Policy"} {
				if method == "GET" && w.Header().Get(h) == "" {
					t.Fatal("missing " + h)
				}
			}
			if f.applied.Load() != 0 {
				t.Fatal("choices applied a write")
			}
		})
	}
}
