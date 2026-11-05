// Package analyzer receives signed watch alerts and investigates them through
// a read-only MCP connection and a function-calling model.
package analyzer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is independent of the opsgate MCP server configuration.
type Config struct {
	Report struct {
		Language       string `yaml:"language"`
		SuggestActions bool   `yaml:"suggest_actions"`
		ApprovalURL    string `yaml:"approval_url"`
	} `yaml:"report"`
	Listen     string `yaml:"listen"`
	SecretFile string `yaml:"secret_file"`
	MCP        struct {
		Endpoint  string `yaml:"endpoint"`
		TokenFile string `yaml:"token_file"`
	} `yaml:"mcp"`
	Model struct {
		Backend string `yaml:"backend"`
		Codex   struct {
			Binary    string `yaml:"binary"`
			ConfigDir string `yaml:"config_dir"`
		} `yaml:"codex"`
		Claude struct {
			Binary       string  `yaml:"binary"`
			ConfigDir    string  `yaml:"config_dir"`
			MaxTurns     int     `yaml:"max_turns"`
			MaxBudgetUSD float64 `yaml:"max_budget_usd"`
		} `yaml:"claude"`
		Endpoint   string `yaml:"endpoint"`
		Name       string `yaml:"name"`
		APIKeyFile string `yaml:"api_key_file"`
	} `yaml:"model"`
	Forward struct {
		URL              string `yaml:"url"`
		SecretFile       string `yaml:"secret_file"`
		AllowPrivateHTTP bool   `yaml:"allow_private_http"`
	} `yaml:"forward"`
	DiscordURLFile      string        `yaml:"discord_url_file"`
	WarningsOnly        bool          `yaml:"warnings_only"`
	BatchWindow         time.Duration `yaml:"batch_window"`
	Cooldown            time.Duration `yaml:"cooldown"`
	Timeout             time.Duration `yaml:"timeout"`
	QueueSize           int           `yaml:"queue_size"`
	MaxToolCalls        int           `yaml:"max_tool_calls"`
	MaxRounds           int           `yaml:"max_rounds"`
	MaxCompletionTokens int           `yaml:"max_completion_tokens"`
	MaxTotalTokens      int           `yaml:"max_total_tokens"`
	MaxContextBytes     int           `yaml:"max_context_bytes"`
	ToolOutputBytes     int           `yaml:"tool_output_bytes"`
}

// Default returns bounded investigation settings. No model is selected implicitly.
func Default() Config {
	c := Config{Listen: "127.0.0.1:8080", WarningsOnly: true, BatchWindow: 10 * time.Second, Cooldown: 15 * time.Minute, Timeout: 2 * time.Minute, QueueSize: 64, MaxToolCalls: 12, MaxRounds: 8, MaxCompletionTokens: 1500, MaxTotalTokens: 100000, MaxContextBytes: 128 << 10, ToolOutputBytes: 16 << 10}
	c.Report.Language = "ko"
	c.Report.SuggestActions = true
	c.Model.Backend = "openai_compatible"
	c.Model.Claude.MaxTurns = 3
	c.Model.Claude.MaxBudgetUSD = 0.5
	c.Model.Endpoint = "https://api.openai.com/v1/chat/completions"
	return c
}

// Load strictly parses one YAML document and validates configuration only.
func Load(path string) (Config, error) {
	c := Default()
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("cannot read analyzer config")
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid analyzer YAML: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("analyzer config must contain one YAML document")
	}
	return c, c.Validate()
}

// Validate rejects invalid URLs, missing credential paths and unbounded settings.
func (c Config) Validate() error {
	if c.Report.Language != "ko" && c.Report.Language != "en" {
		return fmt.Errorf("report.language must be ko or en")
	}
	if c.Report.ApprovalURL != "" {
		u, err := url.Parse(c.Report.ApprovalURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
			return fmt.Errorf("report.approval_url must be an https origin")
		}
	}
	if (c.Forward.URL == "") != (c.Forward.SecretFile == "") {
		return fmt.Errorf("forward.url and forward.secret_file must be configured together")
	}
	_, port, listenErr := net.SplitHostPort(c.Listen)
	portNumber, portErr := strconv.Atoi(port)
	if listenErr != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid listen address")
	}
	if !endpoint(c.MCP.Endpoint) || c.Model.Backend != "claude_cli" && c.Model.Backend != "codex_cli" && !endpoint(c.Model.Endpoint) {
		return fmt.Errorf("MCP and model endpoints require https or loopback http, without credentials, query or fragment")
	}
	if c.SecretFile == "" || c.MCP.TokenFile == "" || c.DiscordURLFile == "" || strings.TrimSpace(c.Model.Name) == "" {
		return fmt.Errorf("secret_file, mcp.token_file, model.name and discord_url_file are required")
	}
	switch c.Model.Backend {
	case "", "openai_compatible":
		if c.Model.APIKeyFile == "" {
			return fmt.Errorf("model.api_key_file is required for openai_compatible")
		}
	case "claude_cli":
		if c.Model.Claude.Binary != "" && !filepath.IsAbs(c.Model.Claude.Binary) || c.Model.Claude.ConfigDir != "" && !filepath.IsAbs(c.Model.Claude.ConfigDir) {
			return fmt.Errorf("model.claude binary and config_dir must be absolute paths when set")
		}
		if c.Model.Claude.MaxTurns < 1 || c.Model.Claude.MaxTurns > 8 || math.IsNaN(c.Model.Claude.MaxBudgetUSD) || math.IsInf(c.Model.Claude.MaxBudgetUSD, 0) || c.Model.Claude.MaxBudgetUSD <= 0 || c.Model.Claude.MaxBudgetUSD > 10 {
			return fmt.Errorf("model.claude max_turns must be 1–8 and max_budget_usd greater than 0 and at most 10")
		}
	case "codex_cli":
		if c.Model.Codex.Binary != "" && !filepath.IsAbs(c.Model.Codex.Binary) || c.Model.Codex.ConfigDir != "" && !filepath.IsAbs(c.Model.Codex.ConfigDir) {
			return fmt.Errorf("model.codex binary and config_dir must be absolute paths when set")
		}
	default:
		return fmt.Errorf("model.backend must be openai_compatible, claude_cli or codex_cli")
	}
	if c.BatchWindow < time.Second || c.BatchWindow > time.Minute || c.Cooldown < time.Minute || c.Cooldown > 24*time.Hour || c.Timeout < time.Second || c.Timeout > 10*time.Minute {
		return fmt.Errorf("invalid batch_window (1s–1m), cooldown (1m–24h) or timeout (1s–10m)")
	}
	if c.QueueSize < 1 || c.QueueSize > 256 || c.MaxToolCalls < 1 || c.MaxToolCalls > 50 || c.MaxRounds < 2 || c.MaxRounds > 20 || c.MaxCompletionTokens < 128 || c.MaxCompletionTokens > 8192 || c.MaxTotalTokens < 4096 || c.MaxTotalTokens <= c.MaxCompletionTokens || c.MaxTotalTokens > 1000000 || c.MaxContextBytes < 4096 || c.MaxContextBytes > 1<<20 || c.ToolOutputBytes < 1024 || c.ToolOutputBytes > 64<<10 {
		return fmt.Errorf("invalid analyzer capacity or investigation limits")
	}
	return nil
}

func endpoint(value string) bool {
	u, err := url.Parse(value)
	if err != nil || len(value) > 4096 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}

func credential(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read credential file")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || len(value) > 16384 {
		return "", fmt.Errorf("credential must contain 1–16384 bytes")
	}
	for _, c := range []byte(value) {
		if c < 32 || c == 127 {
			return "", fmt.Errorf("credential contains control characters")
		}
	}
	return value, nil
}

func httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
