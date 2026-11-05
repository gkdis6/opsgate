package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	if filepath.Base(os.Args[0]) != "codex-fixture" {
		return
	}
	os.Exit(codexFixture())
}
func codexFixture() int {
	args := os.Args[1:]
	value := func(flag string) string {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			return "MISSING"
		}
		return args[i+1]
	}
	for _, flag := range []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "--strict-config"} {
		if !slices.Contains(args, flag) {
			return 90
		}
	}
	if value("--sandbox") != "read-only" || slices.Contains(args, "--dangerously-bypass-approvals-and-sandbox") || os.Getenv("OPSGATE_FIXTURE_SECRET") != "" || os.Getenv("CODEX_HOME") == "" {
		return 91
	}
	for _, setting := range codexFlags {
		if !slices.Contains(args, setting) {
			return 92
		}
	}
	raw, err := os.ReadFile(value("--output-schema"))
	if err != nil || string(raw) != cliReplySchema {
		return 93
	}
	var catalogPath string
	for _, arg := range args {
		if strings.HasPrefix(arg, "model_catalog_json=") {
			_ = json.Unmarshal([]byte(strings.TrimPrefix(arg, "model_catalog_json=")), &catalogPath)
		}
	}
	raw, err = os.ReadFile(catalogPath)
	if err != nil || !strings.Contains(string(raw), `"shell_type":"disabled"`) || !strings.Contains(string(raw), `"apply_patch_tool_type":null`) {
		return 94
	}
	cwd, _ := os.Getwd()
	if !strings.HasPrefix(filepath.Base(cwd), "opsgate-codex-") {
		return 95
	}
	raw, err = io.ReadAll(os.Stdin)
	if err != nil || !json.Valid(raw) {
		return 96
	}
	for _, arg := range args {
		if strings.Contains(arg, "input-only-secret") {
			return 97
		}
	}
	var envelope struct {
		Messages []message `json:"messages"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return 98
	}
	mode := value("--model")
	switch mode {
	case "timeout":
		time.Sleep(30 * time.Second)
		return 0
	case "error":
		fmt.Fprint(os.Stderr, "private-provider-error")
		return 1
	case "bad-json":
		fmt.Println("private-provider-error")
		return 0
	case "failed":
		fmt.Println(`{"type":"turn.failed","error":{"message":"private-provider-error"}}`)
		return 0
	case "native":
		fmt.Println(`{"type":"item.started","item":{"type":"command_execution","command":"DO NOT EXECUTE"}}`)
		time.Sleep(30 * time.Second)
		return 0
	case "huge":
		fmt.Println(strings.Repeat("x", 2<<20))
		return 0
	}
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	emit(map[string]any{"type": "thread.started", "thread_id": "fixture"})
	emit(map[string]any{"type": "turn.started"})
	ready := false
	for _, m := range envelope.Messages {
		if m.Role == "tool" && m.CallID != "seed_notes" {
			ready = true
		}
	}
	content := "확인된 사실: OOMKilled [E2]"
	calls := []map[string]string{}
	if mode == "tools" || mode == "pipeline" && !ready {
		content = ""
		calls = append(calls, map[string]string{"name": "k8s_get", "arguments": `{"kind":"pods","name":"web","namespace":"apps"}`})
	}
	if mode == "write" {
		calls = append(calls, map[string]string{"name": "k8s_rollout_restart", "arguments": "{}"})
	}
	structured, _ := json.Marshal(map[string]any{"content": content, "tool_calls": calls})
	if mode == "schema-missing" {
		structured = []byte(`{"content":"missing tool_calls"}`)
	}
	emit(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(structured)}})
	if mode == "no-completion" {
		return 0
	}
	usage := map[string]int{"input_tokens": 100, "cached_input_tokens": 80, "output_tokens": 20}
	if mode == "negative" {
		usage["input_tokens"] = -1
	}
	if mode == "missing-usage" {
		usage = map[string]int{}
	}
	emit(map[string]any{"type": "turn.completed", "usage": usage})
	return 0
}
func fixtureCodex(t *testing.T, mode string) *codexModel {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	binary := filepath.Join(folder, "codex-fixture")
	if err := os.Symlink(exe, binary); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Model.Backend = "codex_cli"
	c.Model.Name = mode
	c.Model.Codex.Binary = binary
	c.Model.Codex.ConfigDir = folder
	model, err := newCodex(c)
	if err != nil {
		t.Fatal(err)
	}
	return model.(*codexModel)
}
func TestCodexBackendWire(t *testing.T) {
	t.Setenv("OPSGATE_FIXTURE_SECRET", "do-not-inherit")
	model := fixtureCodex(t, "tools")
	messages := []message{{Role: "user", Content: "input-only-secret"}}
	first, err := model.Complete(context.Background(), messages, nil, 1500)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.Complete(context.Background(), messages, nil, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if first.Tokens != 120 || len(first.Message.Calls) != 1 || first.Message.Calls[0].Function.Name != "k8s_get" || first.Message.Calls[0].ID == second.Message.Calls[0].ID {
		t.Fatalf("invalid reply %+v", first)
	}
}
func TestCodexFailures(t *testing.T) {
	for _, mode := range []string{"bad-json", "failed", "error", "huge", "schema-missing", "no-completion", "negative", "missing-usage"} {
		t.Run(mode, func(t *testing.T) {
			model := fixtureCodex(t, mode)
			_, err := model.Complete(context.Background(), nil, nil, 1500)
			if err == nil || strings.Contains(err.Error(), "private-provider-error") {
				t.Fatal(err)
			}
		})
	}
}
func TestCodexNativeToolsAndTimeout(t *testing.T) {
	for _, mode := range []string{"native", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 3 * time.Second
			if mode == "timeout" {
				timeout = 150 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			model := fixtureCodex(t, mode)
			start := time.Now()
			_, err := model.Complete(ctx, nil, nil, 1500)
			if err == nil || time.Since(start) > 2*time.Second {
				t.Fatal("native tool stream or timeout did not abort promptly")
			}
		})
	}
	model := fixtureCodex(t, "tools")
	model.maxBytes = 10
	if _, err := model.Complete(context.Background(), nil, nil, 1500); err == nil {
		t.Fatal("oversized prompt accepted")
	}
}
func TestCodexSharedEnginePolicy(t *testing.T) {
	for _, mode := range []string{"pipeline", "write"} {
		t.Run(mode, func(t *testing.T) {
			model := fixtureCodex(t, mode)
			session := &fakeSession{}
			e := engine{cfg: Default(), model: model, dial: func(context.Context) (toolSession, error) { return session, nil }}
			result, err := e.investigate(context.Background(), testBatch(time.Now()))
			if mode == "pipeline" {
				if err != nil || result.Calls != 2 || !strings.Contains(result.Text, "k8s_get") {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("Codex write request accepted")
			}
			for _, call := range session.calls {
				if call == "k8s_rollout_restart" {
					t.Fatal("Codex bypassed shared policy")
				}
			}
		})
	}
}
func TestCodexConfiguration(t *testing.T) {
	c := Default()
	c.SecretFile = "/hmac"
	c.MCP.Endpoint = "http://127.0.0.1:8788/mcp"
	c.MCP.TokenFile = "/mcp"
	c.DiscordURLFile = "/discord"
	c.Model.Name = "chosen-model"
	c.Model.Backend = "codex_cli"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Model.Codex.Binary = "relative/codex"
	if err := c.Validate(); err == nil {
		t.Fatal("relative binary accepted")
	}
	c.Model.Codex.Binary = "/missing/codex"
	if _, err := newModel(c); err == nil {
		t.Fatal("missing executable accepted")
	}
}
