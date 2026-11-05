package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Optional contract test against an installed Codex binary and a local fake
// Responses endpoint. It makes no paid model calls and needs no real credentials.
func TestInstalledCodexLocalProtocol(t *testing.T) {
	binary := os.Getenv("OPSGATE_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set OPSGATE_TEST_CODEX_BINARY for installed CLI contract test")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer local-fixture-key" {
			t.Error("unexpected auth header")
			w.WriteHeader(401)
			return
		}
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid request JSON")
			w.WriteHeader(400)
			return
		}
		if len(body.Tools) != 0 {
			t.Errorf("native tools exposed: %s", body.Tools)
			w.WriteHeader(400)
			return
		}
		requests.Add(1)
		text := `{"content":"확인된 사실: local protocol fixture","tool_calls":[]}`
		item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15, "input_tokens_details": map[string]any{"cached_tokens": 0}}}},
		} {
			raw, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "local-fixture-key")
	old := codexFlags
	codexFlags = append(append([]string{}, old...), `model_provider="fixture"`, fmt.Sprintf(`model_providers.fixture={name="Fixture",base_url=%q,env_key="OPENAI_API_KEY",wire_api="responses",request_max_retries=0,stream_max_retries=0}`, server.URL+"/v1"), "features.enable_request_compression=false")
	defer func() { codexFlags = old }()
	c := Default()
	c.Model.Name = "opsgate-fixture"
	c.Model.Codex.Binary = binary
	c.Model.Codex.ConfigDir = t.TempDir()
	model, err := newCodex(c)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic, events bytes.Buffer
	model.(*codexModel).stderr = &diagnostic
	model.(*codexModel).protocolDiagnostic = &events
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	reply, err := model.Complete(ctx, []message{{Role: "user", Content: "local protocol fixture"}}, nil, 1500)
	if err != nil {
		t.Logf("local fixture diagnostic: %s; events: %s", diagnostic.String(), events.String())
		t.Fatalf("installed Codex contract: %v (requests=%d)", err, requests.Load())
	}
	if requests.Load() != 1 || reply.Tokens != 15 || len(reply.Message.Calls) != 0 {
		t.Fatalf("requests=%d reply=%+v", requests.Load(), reply)
	}
}
