package analyzer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestModelProtocol(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("missing auth")
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["model"] != "chosen-model" || in["max_completion_tokens"] != float64(1500) || in["parallel_tool_calls"] != false || in["store"] != false {
			t.Error("invalid model request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"total_tokens":100}}`))
	}))
	defer srv.Close()
	m := chatModel{client: httpClient(0), endpoint: srv.URL, key: "private-key", name: "chosen-model", maxBytes: 4096}
	out, err := m.Complete(context.Background(), []message{{Role: "user", Content: "test"}}, nil, 1500)
	if err != nil || out.Tokens != 100 || out.Message.Content != "done" {
		t.Fatalf("%+v %v", out, err)
	}
	_, err = m.Complete(context.Background(), []message{{Role: "user", Content: strings.Repeat("x", 5000)}}, nil, 1500)
	if err == nil || requests != 1 {
		t.Fatal("oversize request reached server")
	}
}
func TestModelErrorsAndRedirects(t *testing.T) {
	for _, body := range []string{`{}`, `{"choices":[{"message":{"content":"secret"},"finish_reason":"length"}],"usage":{"total_tokens":100}}`, strings.Repeat("x", 1<<20+1)} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		m := chatModel{client: httpClient(0), endpoint: srv.URL, maxBytes: 4096}
		if _, err := m.Complete(context.Background(), nil, nil, 100); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
		srv.Close()
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer srv.Close()
	m := chatModel{client: httpClient(0), endpoint: srv.URL, maxBytes: 4096}
	if _, err := m.Complete(context.Background(), nil, nil, 100); err == nil {
		t.Fatal("redirect accepted")
	}
}
func TestUnicodeNotificationChunks(t *testing.T) {
	text := strings.Repeat("한😀글\n", 3000)
	parts := chunks(text, 1900)
	if strings.Join(parts, "") != text {
		t.Fatal("notification content lost")
	}
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) > 1900 || strings.ContainsRune(part, '�') {
			t.Fatal("invalid Unicode chunk")
		}
	}
}
