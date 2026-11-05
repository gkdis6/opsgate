package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type codexModel struct {
	binary, name, configDir string
	maxBytes                int
	sequence                atomic.Uint64
	stderr                  io.Writer
	protocolDiagnostic      io.Writer
}

func newCodex(c Config) (modelClient, error) {
	binary := c.Model.Codex.Binary
	if binary == "" {
		var err error
		binary, err = exec.LookPath("codex")
		if err != nil {
			return nil, fmt.Errorf("model Codex CLI executable not found")
		}
	}
	absolute, err := filepath.Abs(binary)
	if err != nil {
		return nil, fmt.Errorf("invalid model Codex CLI path")
	}
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return nil, fmt.Errorf("model Codex CLI executable is unavailable")
	}
	if c.Model.Codex.ConfigDir != "" {
		info, err := os.Stat(c.Model.Codex.ConfigDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("model Codex CLI config_dir is unavailable")
		}
	}
	return &codexModel{binary: absolute, name: c.Model.Name, configDir: c.Model.Codex.ConfigDir, maxBytes: c.MaxContextBytes}, nil
}

// codexFlags use supported config keys rather than a nonexistent all-tools-off
// switch. The local catalog additionally removes model-owned apply_patch tools.
var codexFlags = []string{
	"suppress_unstable_features_warning=true", "approval_policy=\"never\"", "web_search=\"disabled\"", "mcp_servers={}",
	"agents.enabled=false", "features.multi_agent=false", "features.multi_agent_v2=false",
	"features.shell_tool=false", "features.code_mode=false", "features.code_mode_host=false", "features.code_mode_only=false",
	"features.apps=false", "features.plugins=false", "features.remote_plugin=false", "features.plugin_discovery_prefetch=false",
	"features.hooks=false", "features.memories=false", "features.goals=false", "features.view_image=false", "features.image_generation=false",
	"features.skill_search=false", "features.skill_mcp_dependency_install=false", "features.skip_host_skill_discovery=true",
	"features.tool_suggest=false", "features.current_time_reminder=false", "features.sleep_tool=false", "features.token_budget=false", "features.send_message_to_user_async=false", "features.request_permissions_tool=false",
	"skills.include_instructions=false", "skills.bundled.enabled=false", "plugins._default.enabled=false",
	"tools.update_plan.enabled=false", "tools.experimental_request_user_input.enabled=false",
	"project_doc_max_bytes=0", "notify=[]", "model_provider=\"openai\"",
}

func (m *codexModel) Complete(ctx context.Context, messages []message, tools []definition, maxTokens int) (modelReply, error) {
	input := struct {
		Messages  []message    `json:"messages"`
		Tools     []definition `json:"tools"`
		MaxTokens int          `json:"max_output_tokens"`
	}{messages, tools, maxTokens}
	body, err := json.Marshal(input)
	if err != nil || len(body) > m.maxBytes {
		return modelReply{}, fmt.Errorf("model request exceeds context byte limit")
	}
	folder, err := os.MkdirTemp("", "opsgate-codex-")
	if err != nil {
		return modelReply{}, fmt.Errorf("model Codex CLI workspace unavailable")
	}
	defer os.RemoveAll(folder)
	schemaPath := filepath.Join(folder, "reply.schema.json")
	catalogPath := filepath.Join(folder, "models.json")
	if os.WriteFile(schemaPath, []byte(cliReplySchema), 0600) != nil || os.WriteFile(catalogPath, codexCatalog(m.name), 0600) != nil {
		return modelReply{}, fmt.Errorf("model Codex CLI configuration unavailable")
	}
	quotedPath, _ := json.Marshal(catalogPath)
	quotedInstructions, _ := json.Marshal(cliSystemPrompt)
	args := []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "--ignore-rules", "--strict-config", "--sandbox", "read-only", "--color", "never", "--model", m.name, "--output-schema", schemaPath}
	for _, value := range codexFlags {
		args = append(args, "-c", value)
	}
	args = append(args, "-c", "model_catalog_json="+string(quotedPath), "-c", "developer_instructions="+string(quotedInstructions), "-")
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, m.binary, args...)
	cmd.Dir = folder
	cmd.Env = codexEnv(m.configDir)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stderr = m.stderr
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	cmd.WaitDelay = time.Second
	output := codexOutput{cancel: cancel}
	cmd.Stdout = &output
	if m.protocolDiagnostic != nil {
		cmd.Stdout = io.MultiWriter(m.protocolDiagnostic, &output)
	}
	cancelCLIGroup(cmd)
	if err := cmd.Run(); err != nil || output.err != nil {
		if m.protocolDiagnostic != nil {
			fmt.Fprintf(m.protocolDiagnostic, "\nfixture process: %v; stream: %v\n", err, output.err)
		}
		return modelReply{}, fmt.Errorf("model Codex CLI failed, attempted a native tool or timed out")
	}
	if len(bytes.TrimSpace(output.pending)) != 0 {
		if err := output.event(output.pending); err != nil {
			return modelReply{}, err
		}
	}
	if !output.completed || output.text == "" {
		return modelReply{}, fmt.Errorf("model Codex CLI missing completion or structured reply")
	}
	var structured struct {
		Content *string `json:"content"`
		Calls   *[]struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(output.text), &structured) != nil || structured.Content == nil || structured.Calls == nil || len(*structured.Calls) > 50 || len(*structured.Content) > 80000 {
		return modelReply{}, fmt.Errorf("model Codex CLI invalid structured reply")
	}
	reply := modelReply{Tokens: output.tokens, Message: message{Role: "assistant", Content: *structured.Content}}
	sequence := m.sequence.Add(1)
	for i, call := range *structured.Calls {
		reply.Message.Calls = append(reply.Message.Calls, toolCall{ID: fmt.Sprintf("codex_%d_%d", sequence, i), Type: "function", Function: function{Name: call.Name, Arguments: call.Arguments}})
	}
	return reply, nil
}

// Catalog fields follow Codex 0.166's model metadata schema. An exact slug
// match disables shell, patch and model-suggested tools before inference starts.
func codexCatalog(name string) []byte {
	catalog := map[string]any{"models": []any{map[string]any{
		"slug": name, "display_name": name, "description": "opsgate model adapter", "supported_reasoning_levels": []any{},
		"shell_type": "disabled", "visibility": "list", "supported_in_api": true, "priority": 1,
		"model_messages":    map[string]any{"persistent_instructions": "", "instructions_template": cliSystemPrompt},
		"support_verbosity": false, "apply_patch_tool_type": nil,
		"truncation_policy":            map[string]any{"mode": "bytes", "limit": 131072},
		"experimental_supported_tools": []string{}, "input_modalities": []string{"text"},
		"include_skills_usage_instructions": false, "include_plugin_usage_instructions": false, "include_apps_usage_instructions": false,
		"node_repl_disabled": true, "tool_mode": "direct",
	}}}
	raw, _ := json.Marshal(catalog)
	return raw
}

type codexOutput struct {
	buffer             cappedCLIOutput
	pending            []byte
	cancel             context.CancelFunc
	err                error
	text               string
	tokens             int
	started, completed bool
}

func (o *codexOutput) Write(p []byte) (int, error) {
	if o.err != nil {
		return 0, o.err
	}
	if _, err := o.buffer.Write(p); err != nil {
		o.err = err
		o.cancel()
		return 0, err
	}
	o.pending = append(o.pending, p...)
	for {
		i := bytes.IndexByte(o.pending, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(o.pending[:i])
		o.pending = o.pending[i+1:]
		if len(line) == 0 {
			continue
		}
		if err := o.event(line); err != nil {
			o.err = err
			o.cancel()
			return 0, err
		}
	}
	return len(p), nil
}
func (o *codexOutput) event(raw []byte) error {
	var e struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Cached int `json:"cached_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return fmt.Errorf("model Codex CLI invalid JSONL")
	}
	if o.completed {
		return fmt.Errorf("model Codex CLI events after completion")
	}
	switch e.Type {
	case "thread.started":
	case "turn.started":
		if o.started {
			return fmt.Errorf("model Codex CLI unexpected extra turn")
		}
		o.started = true
	case "item.started", "item.updated", "item.completed":
		if e.Item.Type != "agent_message" && e.Item.Type != "reasoning" {
			return fmt.Errorf("model Codex CLI attempted a native tool")
		}
		if e.Type == "item.completed" && e.Item.Type == "agent_message" {
			o.text = e.Item.Text
		}
	case "turn.completed":
		if !o.started || e.Usage.Input < 0 || e.Usage.Output < 0 || e.Usage.Cached < 0 || e.Usage.Input > 100000000 || e.Usage.Output > 100000000-e.Usage.Input || e.Usage.Cached > e.Usage.Input || e.Usage.Input+e.Usage.Output == 0 {
			return fmt.Errorf("model Codex CLI missing or invalid usage")
		}
		// Cached input is already included in input_tokens; do not count it twice.
		o.tokens = e.Usage.Input + e.Usage.Output
		o.completed = true
	default:
		return fmt.Errorf("model Codex CLI unsuccessful or unsupported event")
	}
	return nil
}

func codexEnv(configDir string) []string {
	allowed := map[string]bool{"HOME": true, "PATH": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true, "CODEX_HOME": true, "CODEX_API_KEY": true, "OPENAI_API_KEY": true}
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if allowed[key] && !(key == "CODEX_HOME" && configDir != "") {
			env = append(env, item)
		}
	}
	if configDir != "" {
		env = append(env, "CODEX_HOME="+configDir)
	}
	return append(env, "NO_COLOR=1", "TERM=dumb")
}

var _ io.Writer = (*codexOutput)(nil)
