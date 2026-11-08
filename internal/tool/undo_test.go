package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
)

func TestSplitUndo(t *testing.T) {
	for in, want := range map[string][2]string{
		"done":                              {"done", ""},
		"done\nundo_url: /approve/action/x": {"done", "/approve/action/x"},
		"a\nundo_url: /x\nmore":             {"a\nundo_url: /x\nmore", ""},
		"undo_url: /x":                      {"undo_url: /x", ""},
	} {
		text, link := SplitUndo(in)
		if text != want[0] || link != want[1] {
			t.Fatalf("%q: %q %q", in, text, link)
		}
	}
}

func TestDirectRunOffersUndo(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.log")
	logger, err := audit.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var built []Undo
	env := &Env{Timeout: 30 * time.Second, MaxOutputBytes: 4096, Audit: logger, UndoURL: func(u Undo) string {
		built = append(built, u)
		return "https://ops.example/approve/action/" + u.Tool + "?replicas=3"
	}}
	undo := &Undo{Tool: "k8s_scale", Args: map[string]any{"replicas": 3}}
	AddWrite(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil), env, WriteSpec{Name: "w", Direct: true}, WriteHandler[writeTestInput]{
		Plan: func(context.Context, Caller, writeTestInput) (Plan, error) {
			return Plan{Summary: "scale", Undo: undo}, nil
		},
		Apply: func(context.Context, Caller, writeTestInput, Plan) (string, error) { return "scaled", nil },
	})
	caller := Caller{User: "a", Transport: "web", Scopes: []string{config.ScopeWrite}}
	text, err := env.Direct("w").Run(context.Background(), caller, json.RawMessage(`{"target":"web"}`), "scale")
	if err != nil || text != "scaled\nundo_url: https://ops.example/approve/action/k8s_scale?replicas=3" || len(built) != 1 || built[0].Tool != "k8s_scale" {
		t.Fatalf("%q %v %+v", text, err, built)
	}
	logger.Close()
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), `"undo":true`) {
		t.Fatalf("audit lacks undo flag: %s", data)
	}

	// Without a link builder the plan's undo is silently dropped.
	env.UndoURL = nil
	env.Audit = nil
	if line := env.undoLine(Plan{Undo: undo}); line != "" {
		t.Fatalf("undo without builder: %q", line)
	}
	env.UndoURL = func(Undo) string { return "/x\nundo_url: /y" }
	if line := env.undoLine(Plan{Undo: undo}); line != "" {
		t.Fatalf("multi-line link accepted: %q", line)
	}
}
