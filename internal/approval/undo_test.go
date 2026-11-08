package approval

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestSplitUndoOnlyLocalCatalogLinks(t *testing.T) {
	m := &Manager{publicURL: "https://ops.example"}
	const link = "/approve/action/k8s_scale?kind=deployment&name=web&namespace=apps&replicas=3&source=rule"
	for name, tc := range map[string]struct{ in, text, local string }{
		"absolute":       {"scaled\nundo_url: https://ops.example" + link, "scaled", link},
		"relative":       {"scaled\nundo_url: " + link, "scaled", link},
		"foreign host":   {"scaled\nundo_url: https://evil.example" + link, "scaled", ""},
		"http downgrade": {"scaled\nundo_url: http://ops.example" + link, "scaled", ""},
		"not catalog":    {"scaled\nundo_url: /approve/action/note_trash?path=a", "scaled", ""},
		"credentials":    {"scaled\nundo_url: https://u@ops.example" + link, "scaled", ""},
		"no undo":        {"scaled", "scaled", ""},
	} {
		text, local, _ := m.splitUndo(tc.in)
		if text != tc.text || local != tc.local {
			t.Fatalf("%s: %q %q", name, text, local)
		}
	}
}

func TestApprovedScaleShowsUndoButton(t *testing.T) {
	type input struct {
		tool.Approval
		Cluster   string `json:"cluster,omitempty"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Replicas  int32  `json:"replicas"`
	}
	env := &tool.Env{Timeout: time.Second, MaxOutputBytes: 4096}
	env.UndoURL = func(u tool.Undo) string {
		link, err := action.URL("https://ops.example", u.Tool, u.Args, "rule")
		if err != nil {
			t.Fatal(err)
		}
		return link
	}
	tool.AddWrite(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), env,
		tool.WriteSpec{Name: "k8s_scale", Title: "Scale workload", Direct: true},
		tool.WriteHandler[input]{
			Plan: func(_ context.Context, _ tool.Caller, in input) (tool.Plan, error) {
				return tool.Plan{Summary: "Scale web 2 → 5", Undo: &tool.Undo{Tool: "k8s_scale", Args: map[string]any{"kind": in.Kind, "namespace": in.Namespace, "name": in.Name, "replicas": int32(2)}}}, nil
			},
			Apply: func(context.Context, tool.Caller, input, tool.Plan) (string, error) { return "Scaled web to 5", nil },
		})
	f := &restartFixture{}
	f.manager = New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true}, "https://ops.example", writer(), nil)
	f.manager.SetDirect(env.Direct)
	f.mux = http.NewServeMux()
	f.manager.Routes(f.mux)

	opened := f.do("GET", "/approve/action/k8s_scale?kind=deployment&namespace=apps&name=web&replicas=5", "alice", nil)
	if opened.Code != http.StatusSeeOther {
		t.Fatalf("open: %d %s", opened.Code, opened.Body)
	}
	page := f.do("GET", opened.Header().Get("Location"), "alice", nil)
	if strings.Contains(page.Body.String(), ">Undo<") {
		t.Fatal("undo shown before approval")
	}
	csrf := csrfValue.FindStringSubmatch(page.Body.String())
	result := f.do("POST", opened.Header().Get("Location"), "alice", url.Values{"csrf": {csrf[1]}, "decision": {"approve"}})
	body := result.Body.String()
	if result.Code != 200 || !strings.Contains(body, "Scaled web to 5") || strings.Contains(body, "undo_url") ||
		!strings.Contains(body, `href="/approve/action/k8s_scale?`) || !strings.Contains(body, "replicas=2") {
		t.Fatalf("%d %s", result.Code, body)
	}
	inbox := f.do("GET", "/approvals", "alice", nil)
	if !strings.Contains(inbox.Body.String(), `href="/approve/action/k8s_scale?`) {
		t.Fatalf("inbox lacks undo: %s", inbox.Body)
	}
}
