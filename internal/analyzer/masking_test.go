package analyzer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type maskingTransport func(*http.Request) (*http.Response, error)

func (f maskingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAlertMaskingAtModelBoundaries(t *testing.T) {
	for _, backend := range []string{"api", "claude", "codex"} {
		for _, reason := range []string{`password="alertcanary plain"`, `password="prefix \"alertcanary\" suffix"`, "password=\"alertcanary\\path\""} {
			t.Run(backend+"/"+reason, func(t *testing.T) {
				var model modelClient
				switch backend {
				case "claude":
					model = fixtureModel(t, "masked-alert")
				case "codex":
					model = fixtureCodex(t, "masked-alert")
				default:
					turn := 0
					model = chatModel{client: &http.Client{Transport: maskingTransport(func(r *http.Request) (*http.Response, error) {
						b, _ := io.ReadAll(r.Body)
						if strings.Contains(string(b), "alertcanary") {
							t.Error("unmasked alert reached API")
						}
						var request struct {
							Messages []message `json:"messages"`
						}
						if json.Unmarshal(b, &request) != nil {
							t.Fatal("bad API JSON")
						}
						for _, m := range request.Messages {
							if m.Role == "user" && strings.HasPrefix(m.Content, "Investigate this signed") {
								_, envelope, _ := strings.Cut(m.Content, "\n")
								if !json.Valid([]byte(envelope)) {
									t.Error("masking corrupted alert JSON")
								}
							}
						}
						turn++
						body := `{"choices":[{"finish_reason":"stop","message":{"content":"done"}}],"usage":{"total_tokens":10}}`
						if turn == 1 {
							body = `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"read","type":"function","function":{"name":"k8s_get","arguments":"{}"}}]}}],"usage":{"total_tokens":10}}`
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
					})}, endpoint: "https://model.example/", maxBytes: 128 << 10}
				}
				b := testBatch(time.Now())
				b.Alerts[0].Reason = reason
				// Seeded note lookups also echo alert fields into assistant tool
				// arguments; those presentations must not reintroduce the secret.
				b.Alerts[0].Namespace = reason
				e := engine{cfg: Default(), model: model, dial: func(context.Context) (toolSession, error) { return &fakeSession{}, nil }}
				if _, err := e.investigate(context.Background(), b); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
