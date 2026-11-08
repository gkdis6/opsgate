package approval

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestHostOptionsPage(t *testing.T) {
	type input struct {
		tool.Approval
		Unit string `json:"unit"`
	}
	env := &tool.Env{Timeout: time.Second, MaxOutputBytes: 4096}
	tool.AddWrite(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), env,
		tool.WriteSpec{Name: "host_restart_unit", Title: "Restart systemd unit", Direct: true},
		tool.WriteHandler[input]{
			Plan: func(_ context.Context, _ tool.Caller, in input) (tool.Plan, error) {
				if in.Unit != "app.service" {
					return tool.Plan{}, tool.Denied("not allowed")
				}
				return tool.Plan{Summary: "Restart systemd unit " + in.Unit}, nil
			},
			Apply: func(context.Context, tool.Caller, input, tool.Plan) (string, error) { return "restarted", nil },
		})
	f := &restartFixture{}
	f.manager = New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true}, "https://ops.example", writer(), nil)
	f.manager.SetDirect(env.Direct)
	var asked []string
	f.manager.SetHostOptions(func(_ context.Context, _ tool.Caller, kind, name string) (action.Options, error) {
		asked = append(asked, kind+"/"+name)
		choice := func(unit string) action.Choice {
			link, _ := action.URL("", "host_restart_unit", map[string]any{"unit": unit}, "rule")
			return action.Choice{Tool: "host_restart_unit", URL: link, Reason: "failed", ReasonKO: "실패"}
		}
		// The second choice names another unit and must be dropped.
		return action.Options{Actions: []action.Choice{choice(name), choice("other.service")}}, nil
	})
	f.mux = http.NewServeMux()
	f.manager.Routes(f.mux)

	page := f.do("GET", "/approve/host-options/unit/app.service", "alice", nil)
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, `href="/approve/action/host_restart_unit?source=rule&amp;unit=app.service"`) || strings.Contains(body, "other.service") {
		t.Fatalf("%d %s", page.Code, body)
	}
	for _, target := range []string{"/approve/host-options/unit/app", "/approve/host-options/pod/web", "/approve/host-options/unit/app.service?x=1"} {
		if w := f.do("GET", target, "alice", nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	if len(asked) != 1 {
		t.Fatalf("rule engine called for invalid links: %v", asked)
	}
	reader := &restartFixture{manager: New(config.Approval{TTL: time.Minute, Web: true, RequireWeb: true}, "https://ops.example", accessBrowser{scopes: []string{config.ScopeRead}, known: true}, nil)}
	reader.manager.SetDirect(env.Direct)
	reader.manager.SetHostOptions(func(context.Context, tool.Caller, string, string) (action.Options, error) { return action.Options{}, nil })
	reader.mux = http.NewServeMux()
	reader.manager.Routes(reader.mux)
	if w := reader.do("GET", "/approve/host-options/unit/app.service", "bob", nil); w.Code != http.StatusForbidden {
		t.Fatalf("read-only user: %d", w.Code)
	}
}
