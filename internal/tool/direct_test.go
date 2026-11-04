package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
)

func directFixture(t *testing.T, spec WriteSpec, env *Env, summary *string, applied *int) *Direct {
	t.Helper()
	AddWrite(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), env, spec, WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) { return Plan{Summary: *summary}, nil },
		Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) {
			*applied++
			return "done", nil
		},
	})
	return env.Direct(spec.Name)
}

func TestDirectIsOptIn(t *testing.T) {
	summary, applied := "plan", 0
	env := &Env{Timeout: time.Second}
	if directFixture(t, WriteSpec{Name: "plain"}, env, &summary, &applied) != nil {
		t.Fatal("tool is direct without opting in")
	}
	env = &Env{Timeout: time.Second, Policy: NewPolicy(config.ToolPolicy{Deny: []string{"blocked"}})}
	if directFixture(t, WriteSpec{Name: "blocked", Direct: true}, env, &summary, &applied) != nil {
		t.Fatal("a tool disabled by policy stayed reachable")
	}
}

func TestDirectChecksAndAudit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.log")
	logger, err := audit.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	summary, applied := "restart web", 0
	env := &Env{Timeout: time.Second, MaxOutputBytes: 4096, Audit: logger}
	d := directFixture(t, WriteSpec{Name: "w", Direct: true}, env, &summary, &applied)
	ctx := context.Background()
	raw := json.RawMessage(`{"target":"web"}`)
	allowed := Caller{User: "a", Transport: "web", Scopes: []string{config.ScopeWrite}}

	for name, c := range map[string]Caller{
		"no scope":  {User: "a", Transport: "web", Scopes: []string{config.ScopeRead}},
		"tool list": {User: "a", Transport: "web", Scopes: []string{config.ScopeWrite}, Tools: []string{"other"}},
	} {
		if _, _, err := d.Plan(ctx, c, raw); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s plan: %v", name, err)
		}
		if _, err := d.Run(ctx, c, raw, "restart web"); !errors.Is(err, ErrDenied) || applied != 0 {
			t.Fatalf("%s run: %v, applied %d", name, err, applied)
		}
	}
	if _, _, err := d.Plan(ctx, allowed, json.RawMessage(`[1]`)); !errors.Is(err, ErrDenied) {
		t.Fatalf("bad args: %v", err)
	}
	if _, shown, err := d.Plan(ctx, allowed, raw); err != nil || shown != "restart web" {
		t.Fatalf("plan: %q %v", shown, err)
	}
	if _, err := d.Run(ctx, allowed, raw, "restart other"); !errors.Is(err, ErrDenied) || applied != 0 {
		t.Fatalf("stale approval: %v, applied %d", err, applied)
	}
	text, err := d.Run(ctx, allowed, raw, "restart web")
	if err != nil || text != "done" || applied != 1 {
		t.Fatalf("run: %q %v, applied %d", text, err, applied)
	}
	logger.Close()
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, want := range []string{`"outcome":"denied"`, `"outcome":"ok"`, `"transport":"web"`, `"approval":"web"`, `"restart web"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit lacks %s: %s", want, log)
		}
	}
}
