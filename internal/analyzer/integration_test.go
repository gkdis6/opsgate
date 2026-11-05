package analyzer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise signed ingress -> queue -> real MCP HTTP client -> model tool loop
// -> notification, with local fake endpoints only and no paid model calls.
func TestSignedInvestigationPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reads := 0
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "fixture-reader", Version: "1"}, nil)
	mcpServer.AddTool(&mcp.Tool{Name: "k8s_get", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		reads++
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Pod apps/web last termination reason OOMKilled"}}}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{})
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer reader-token" {
			w.WriteHeader(401)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer reader.Close()
	turns := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-model-key" {
			t.Error("wrong model credential")
		}
		var req struct {
			Messages []message    `json:"messages"`
			Tools    []definition `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid model JSON")
		}
		for _, d := range req.Tools {
			if d.Function.Name != "k8s_get" {
				t.Error("unexpected tool")
			}
		}
		turns++
		w.Header().Set("Content-Type", "application/json")
		if turns == 1 {
			w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"read-pod","type":"function","function":{"name":"k8s_get","arguments":"{\"kind\":\"pods\",\"namespace\":\"apps\",\"name\":\"web\"}"}}]}}],"usage":{"total_tokens":100}}`))
			return
		}
		found := false
		for _, m := range req.Messages {
			if m.Role == "tool" && m.CallID == "read-pod" && strings.Contains(m.Content, "OOMKilled") {
				found = true
			}
		}
		if !found {
			t.Error("MCP evidence not returned to model")
		}
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"확인된 사실: Pod가 OOMKilled로 종료됨 [E1]. 추정 원인: 메모리 사용량 증가. 추가 확인 사항: 리소스 제한과 직전 로그."}}],"usage":{"total_tokens":100}}`))
	}))
	defer model.Close()
	received := ""
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Content string `json:"content"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			t.Error("invalid notification")
		}
		received = payload.Content
		w.WriteHeader(204)
	}))
	defer discord.Close()
	dir := t.TempDir()
	c := Default()
	c.MCP.Endpoint = reader.URL + "/mcp"
	c.Model.Endpoint = model.URL
	c.Model.Name = "fixture-model"
	for file, value := range map[string]string{"hmac": "0123456789abcdef", "mcp": "reader-token", "key": "fixture-model-key", "discord": "https://discord.com/api/webhooks/123/test-token"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.SecretFile = filepath.Join(dir, "hmac")
	c.MCP.TokenFile = filepath.Join(dir, "mcp")
	c.Model.APIKeyFile = filepath.Join(dir, "key")
	c.DiscordURLFile = filepath.Join(dir, "discord")
	worker, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.ready(ctx); err != nil {
		t.Fatal(err)
	}
	sender, err := alert.NewWebhook(alert.WebhookOptions{Options: alert.Options{URL: discord.URL}, BodyTemplate: `{"content":{{json .Text}},"allowed_mentions":{"parse":[]}}`})
	if err != nil {
		t.Fatal(err)
	}
	worker.notify = func(ctx context.Context, text string, _ []alert.ActionLink) error {
		_, err := sender.Send(ctx, &alert.Batch{Text: text})
		return err
	}
	now := time.Now().UTC()
	worker.now = func() time.Time { return now }
	b := testBatch(now)
	if status := post(worker, b, false); status != 401 || turns != 0 {
		t.Fatal("unsigned input reached model")
	}
	if status := post(worker, b, true); status != 202 {
		t.Fatal(status)
	}
	if status := post(worker, b, true); status != 202 {
		t.Fatal(status)
	}
	now = now.Add(c.BatchWindow)
	job, ok := worker.take()
	if !ok {
		t.Fatal("no queued investigation")
	}
	worker.process(ctx, job)
	if turns != 2 || reads != 1 || !strings.Contains(received, "[완료]") || !strings.Contains(received, "k8s_get") || !strings.Contains(received, "OOMKilled") {
		t.Fatalf("turns=%d reads=%d received=%s", turns, reads, received)
	}
	if _, ok := worker.take(); ok {
		t.Fatal("replayed batch created another investigation")
	}
}
