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

// The subprocess fixture consumes actual argv/stdin; it never calls a model.
func init() {
	if filepath.Base(os.Args[0]) != "claude-fixture" {
		return
	}
	code := cliFixture()
	os.Exit(code)
}
func cliFixture() int {
	args := os.Args[1:]
	value := func(flag string) string {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			return "MISSING"
		}
		return args[i+1]
	}
	for _, flag := range []string{"--restricted", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence", "--no-chrome", "-p"} {
		if !slices.Contains(args, flag) {
			return 90
		}
	}
	if value("--tools") != "" || value("--setting-sources") != "" || value("--mcp-config") != `{"mcpServers":{}}` || value("--settings") != `{"disableAllHooks":true}` || slices.Contains(args, "--bare") || slices.Contains(args, "--dangerously-skip-permissions") {
		return 91
	}
	if os.Getenv("OPSGATE_FIXTURE_SECRET") != "" || os.Getenv("CLAUDE_CODE_MAX_OUTPUT_TOKENS") != "1500" || os.Getenv("CLAUDE_CODE_SKIP_PROMPT_HISTORY") != "1" || os.Getenv("CLAUDE_CODE_MAX_RETRIES") != "0" {
		return 92
	}
	cwd, _ := os.Getwd()
	if !strings.HasPrefix(filepath.Base(cwd), "opsgate-claude-") {
		return 93
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil || !json.Valid(raw) {
		return 94
	}
	for _, arg := range args {
		if strings.Contains(arg, "input-only-secret") {
			return 95
		}
	}
	mode := value("--model")
	if mode == "masked-alert" && strings.Contains(string(raw), "alertcanary") {
		return 99
	}
	switch mode {
	case "timeout":
		time.Sleep(30 * time.Second)
		return 0
	case "bad-json":
		fmt.Print("private-provider-error")
		return 0
	case "error":
		fmt.Fprint(os.Stderr, "private-provider-error")
		return 1
	case "huge":
		fmt.Print(strings.Repeat("x", 2<<20))
		return 0
	case "schema-missing":
		fmt.Print(`{"type":"result","subtype":"success","result":"text","usage":{"input_tokens":1}}`)
		return 0
	case "budget":
		fmt.Print(`{"type":"result","subtype":"error_max_budget_usd","is_error":true}`)
		return 0
	case "negative":
		fmt.Print(`{"type":"result","subtype":"success","structured_output":{"content":"text","tool_calls":[]},"usage":{"input_tokens":-1}}`)
		return 0
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") == "" {
		return 96
	}
	var input struct {
		Messages []message `json:"messages"`
	}
	_ = json.Unmarshal(raw, &input)
	ready := false
	for _, message := range input.Messages {
		if message.Role == "tool" && message.CallID != "seed_notes" {
			ready = true
		}
	}
	if mode == "write" {
		fmt.Print(`{"type":"result","subtype":"success","structured_output":{"content":"","tool_calls":[{"name":"k8s_rollout_restart","arguments":"{}"}]},"usage":{"input_tokens":10,"output_tokens":20}}`)
		return 0
	}
	if mode == "tools" || (mode == "pipeline" || mode == "masked-alert") && !ready {
		fmt.Print(`{"type":"result","subtype":"success","is_error":false,"structured_output":{"content":"","tool_calls":[{"name":"k8s_get","arguments":"{\"kind\":\"pods\",\"name\":\"web\",\"namespace\":\"apps\"}"}]},"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40}}`)
	} else {
		fmt.Print(`{"type":"result","subtype":"success","is_error":false,"structured_output":{"content":"확인: OOMKilled [E1]","tool_calls":[]},"usage":{"input_tokens":10,"output_tokens":20}}`)
	}
	return 0
}
func fixtureModel(t *testing.T, mode string) *claudeModel {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	binary := filepath.Join(folder, "claude-fixture")
	if err := os.Symlink(exe, binary); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Model.Backend = "claude_cli"
	c.Model.Name = mode
	c.Model.Claude.Binary = binary
	c.Model.Claude.ConfigDir = folder
	m, err := newClaude(c)
	if err != nil {
		t.Fatal(err)
	}
	return m.(*claudeModel)
}
func TestClaudeBackendWire(t *testing.T) {
	t.Setenv("OPSGATE_FIXTURE_SECRET", "do-not-inherit")
	m := fixtureModel(t, "tools")
	msg := []message{{Role: "user", Content: "input-only-secret"}}
	first, err := m.Complete(context.Background(), msg, nil, 1500)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Complete(context.Background(), msg, nil, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if first.Tokens != 100 || len(first.Message.Calls) != 1 || first.Message.Calls[0].Function.Name != "k8s_get" || first.Message.Calls[0].ID == second.Message.Calls[0].ID {
		t.Fatalf("invalid reply: %+v", first)
	}
}
func TestClaudeFailures(t *testing.T) {
	for _, mode := range []string{"bad-json", "error", "huge", "schema-missing", "budget", "negative"} {
		t.Run(mode, func(t *testing.T) {
			m := fixtureModel(t, mode)
			_, err := m.Complete(context.Background(), nil, nil, 1500)
			if err == nil || strings.Contains(err.Error(), "private-provider-error") {
				t.Fatal(err)
			}
		})
	}
}
func TestClaudeTimeoutAndContextBound(t *testing.T) {
	m := fixtureModel(t, "timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := m.Complete(ctx, nil, nil, 1500); err == nil || time.Since(start) > 3*time.Second {
		t.Fatal("CLI timeout did not terminate promptly")
	}
	m = fixtureModel(t, "tools")
	m.maxBytes = 20
	if _, err := m.Complete(context.Background(), []message{{Content: strings.Repeat("x", 100)}}, nil, 1500); err == nil {
		t.Fatal("oversize CLI prompt allowed")
	}
}
func TestBackendConfig(t *testing.T) {
	c := Default()
	c.SecretFile = "/hmac"
	c.MCP.Endpoint = "http://127.0.0.1:8788/mcp"
	c.MCP.TokenFile = "/mcp-token"
	c.DiscordURLFile = "/discord"
	c.Model.Name = "sonnet"
	if err := c.Validate(); err == nil {
		t.Fatal("API backend accepted missing key")
	}
	c.Model.Backend = "claude_cli"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Model.Claude.Binary = "relative/claude"
	if err := c.Validate(); err == nil {
		t.Fatal("relative executable accepted")
	}
	c.Model.Claude.Binary = "/nonexistent/claude"
	if _, err := newModel(c); err == nil {
		t.Fatal("missing CLI accepted")
	}
	c.Model.Backend = "unknown"
	if err := c.Validate(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestClaudeSharedEnginePolicy(t *testing.T) {
	for _, mode := range []string{"pipeline", "write"} {
		t.Run(mode, func(t *testing.T) {
			model := fixtureModel(t, mode)
			session := &fakeSession{}
			e := engine{cfg: Default(), model: model, dial: func(context.Context) (toolSession, error) { return session, nil }}
			result, err := e.investigate(context.Background(), testBatch(time.Now()))
			if mode == "pipeline" {
				if err != nil || result.Calls != 2 || !strings.Contains(result.Text, "k8s_get") {
					t.Fatalf("%+v %v", result, err)
				}
			} else {
				if err == nil {
					t.Fatal("CLI write accepted")
				}
			}
			for _, call := range session.calls {
				if call == "k8s_rollout_restart" {
					t.Fatal("CLI bypassed shared policy")
				}
			}
		})
	}
}

func TestCLIOutputCapCannotBeBypassedByIOCopy(t *testing.T) {
	var output cappedCLIOutput
	source := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 2<<20))}
	if _, err := io.Copy(&output, source); err == nil || len(output.Bytes()) > 1<<20 {
		t.Fatal("CLI output cap was bypassed")
	}
}
