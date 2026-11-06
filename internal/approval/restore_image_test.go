package approval

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type restoreLinkInput struct {
	tool.Approval
	Cluster       string `json:"cluster"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	Container     string `json:"container"`
	ExpectedImage string `json:"expected_image"`
	Image         string `json:"image"`
}

func restoreWebFixture(t *testing.T, browser BrowserAuth) *restartFixture {
	t.Helper()
	f := newRestartFixture(t, browser)
	env := &tool.Env{Timeout: 30 * time.Second, MaxOutputBytes: 4096}
	tool.AddWrite(mcp.NewServer(&mcp.Implementation{Name: "restore", Version: "1"}, nil), env, tool.WriteSpec{Name: "k8s_restore_image", Direct: true, Title: "Restore healthy image"}, tool.WriteHandler[restoreLinkInput]{
		Plan: func(_ context.Context, _ tool.Caller, in restoreLinkInput) (tool.Plan, error) {
			f.planned.Add(1)
			if in.ExpectedImage != "bad" || in.Image != "good" {
				return tool.Plan{}, tool.Denied("invalid verified transition")
			}
			return tool.Plan{Summary: "Restore app bad -> good", NoWindow: true}, nil
		},
		Apply: func(context.Context, tool.Caller, restoreLinkInput, tool.Plan) (string, error) {
			f.applied.Add(1)
			return "Restored image", nil
		},
	})
	f.manager.SetDirect(env.Direct)
	return f
}

const restoreFixtureURL = "/approve/restore-image/main/apps/web?container=app&expected_image=bad&image=good"

func TestRestoreImageRequiresReviewAndApproval(t *testing.T) {
	f := restoreWebFixture(t, writer())
	opened := f.do("GET", restoreFixtureURL, "alice", nil)
	if opened.Code != http.StatusSeeOther || f.applied.Load() != 0 {
		t.Fatal("link applied a write")
	}
	path := opened.Header().Get("Location")
	page := f.do("GET", path, "alice", nil)
	csrf := csrfValue.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "bad -&gt; good") || strings.Contains(page.Body.String(), "approve_window") {
		t.Fatal(page.Body.String())
	}
	denied := f.do("POST", path, "alice", url.Values{"decision": {"approve"}, "csrf": {"wrong"}})
	if denied.Code != http.StatusForbidden || f.applied.Load() != 0 {
		t.Fatal("invalid CSRF applied action")
	}
	result := f.do("POST", path, "alice", url.Values{"decision": {"approve"}, "csrf": {csrf[1]}})
	if result.Code != http.StatusOK || f.applied.Load() != 1 {
		t.Fatalf("status=%d body=%s", result.Code, result.Body)
	}
	_ = f.do("POST", path, "alice", url.Values{"decision": {"approve"}, "csrf": {csrf[1]}})
	if f.applied.Load() != 1 {
		t.Fatal("approval replayed")
	}
}
func TestRestoreImageLinkRefusals(t *testing.T) {
	for _, test := range []struct {
		name, target string
		browser      BrowserAuth
		status       int
	}{
		{"read scope", restoreFixtureURL, accessBrowser{scopes: []string{"ops:read"}, known: true}, 403},
		{"duplicate image", restoreFixtureURL + "&image=other", writer(), 400},
		{"invalid image", strings.Replace(restoreFixtureURL, "image=good", "image=unverified", 1), writer(), 403},
		{"missing parameter", "/approve/restore-image/main/apps/web", writer(), 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := restoreWebFixture(t, test.browser)
			w := f.do("GET", test.target, "alice", nil)
			if w.Code != test.status || f.applied.Load() != 0 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}
