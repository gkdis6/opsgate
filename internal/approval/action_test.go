package approval

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const genericRestart = "/approve/action/k8s_rollout_restart?cluster=main&kind=deployment&namespace=apps&name=web"

func TestGenericLinkTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, target, user string
		browser            BrowserAuth
		status             int
	}{
		{"sign in", genericRestart, "", authenticatedLinkBrowser{writer()}, 401},
		{"read only", genericRestart, "alice", accessBrowser{scopes: []string{"ops:read"}, known: true}, 403},
		{"user tool policy", genericRestart, "alice", accessBrowser{scopes: []string{"ops:write"}, tools: []string{"other"}, known: true}, 403},
		{"unknown tool", "/approve/action/unknown?namespace=apps&name=web", "alice", writer(), 400},
		{"note tool", "/approve/action/note_trash?path=a.md", "alice", writer(), 400},
		{"extra arg", genericRestart + "&unexpected=value", "alice", writer(), 400},
		{"approval injection", genericRestart + "&approval_id=forged", "alice", writer(), 400},
		{"source injection", genericRestart + "&source=ai&source=rule", "alice", writer(), 400},
		{"arg duplicate", genericRestart + "&name=other", "alice", writer(), 400},
		{"masked", strings.Replace(genericRestart, "name=web", "name=%5BREDACTED%5D", 1), "alice", writer(), 400},
		{"legacy injection", "/approve/restart/main/deployment/apps/web?approval_id=forged", "alice", writer(), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRestartFixture(t, tc.browser)
			w := f.do("GET", tc.target, tc.user, nil)
			if w.Code != tc.status || len(f.manager.approvals) != 0 || f.applied.Load() != 0 {
				t.Fatalf("%d: %s", w.Code, w.Body)
			}
		})
	}
}

func TestGenericSourcePageAuditAndExecution(t *testing.T) {
	for _, source := range []string{"", "rule", "ai"} {
		f := newRestartFixture(t, writer())
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		logger, err := audit.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer logger.Close()
		f.manager.audit = logger
		opened := f.do("GET", genericRestart+"&source="+source, "alice", nil)
		if opened.Code != http.StatusSeeOther || f.applied.Load() != 0 {
			t.Fatalf("open: %d", opened.Code)
		}
		page := f.do("GET", opened.Header().Get("Location"), "alice", nil)
		shown, recorded := "rule", "rule"
		if source == "ai" {
			shown, recorded = "AI — review carefully", "ai"
		}
		if !strings.Contains(page.Body.String(), "Restart workload") || !strings.Contains(page.Body.String(), "Suggested by: "+shown) {
			t.Fatal(page.Body.String())
		}
		csrf := csrfValue.FindStringSubmatch(page.Body.String())
		result := f.do("POST", opened.Header().Get("Location"), "alice", url.Values{"csrf": {csrf[1]}, "decision": {"approve"}})
		if result.Code != 200 || f.applied.Load() != 1 {
			t.Fatal(result.Body.String())
		}
		data, _ := os.ReadFile(path)
		if strings.Count(string(data), `"source":"`+recorded+`"`) != 2 {
			t.Fatalf("pending and decision source: %s", data)
		}
	}
}

func TestLegacyRestartAlias(t *testing.T) {
	f := newRestartFixture(t, writer())
	w := f.do("GET", "/approve/restart/main/deployment/apps/web", "alice", nil)
	if w.Code != 303 || f.planned.Load() != 1 || f.applied.Load() != 0 {
		t.Fatalf("alias: %d %s", w.Code, w.Body)
	}
}

type authenticatedLinkBrowser struct{ accessBrowser }

func (b authenticatedLinkBrowser) RequireUser(next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Test-User")
		if user == "" {
			http.Error(w, "Sign in required", http.StatusUnauthorized)
			return
		}
		next(w, r, user)
	})
}

func TestGenericScaleUsesIntegerArguments(t *testing.T) {
	f := newRestartFixture(t, writer())
	env := &tool.Env{Timeout: time.Second, MaxOutputBytes: 4096}
	type input struct {
		tool.Approval
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Replicas  int32  `json:"replicas"`
	}
	tool.AddWrite(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil), env, tool.WriteSpec{Name: "k8s_scale", Direct: true}, tool.WriteHandler[input]{
		Plan: func(_ context.Context, _ tool.Caller, in input) (tool.Plan, error) {
			if in.Replicas != 3 {
				t.Fatalf("integer lost: %v", in)
			}
			return tool.Plan{Summary: "Scale apps/web to 3"}, nil
		},
		Apply: func(context.Context, tool.Caller, input, tool.Plan) (string, error) {
			f.applied.Add(1)
			return "scaled", nil
		},
	})
	f.manager.SetDirect(env.Direct)
	opened := f.do("GET", "/approve/action/k8s_scale?namespace=apps&name=web&kind=deployment&replicas=3&source=ai", "alice", nil)
	if opened.Code != 303 {
		t.Fatal(opened.Body.String())
	}
	page := f.do("GET", opened.Header().Get("Location"), "alice", nil)
	csrf := csrfValue.FindStringSubmatch(page.Body.String())
	if csrf == nil {
		t.Fatal(page.Body.String())
	}
	result := f.do("POST", opened.Header().Get("Location"), "alice", url.Values{"csrf": {csrf[1]}, "decision": {"approve"}})
	if result.Code != 200 || f.applied.Load() != 1 {
		t.Fatal(result.Body.String())
	}
}
