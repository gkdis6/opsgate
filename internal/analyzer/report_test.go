package analyzer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportLanguagesAndProposals(t *testing.T) {
	c := Default()
	ko := reportInstructions(c)
	for _, text := range []string{"Report in Korean", "권장 조치", "placeholder requiring validation", "NEVER tool calls", "Do not recommend a restart"} {
		if !strings.Contains(ko, text) {
			t.Errorf("missing policy %q", text)
		}
	}
	footer := evidenceFooter(c, []string{"[E1] k8s_get"}, 1, 20)
	if !strings.Contains(footer, "조회 근거") {
		t.Fatal(footer)
	}
	c.Report.Language = "en"
	if prompt := reportInstructions(c); !strings.Contains(prompt, "Report in English") || strings.Contains(prompt, "Report in Korean") {
		t.Fatal("English policy not selected")
	}
	text := reportNotification(c, testBatch(time.Now()), investigation{}, "failed", "model_request")
	if !strings.Contains(text, "Analysis failed") || strings.Contains(text, "분석 실패") {
		t.Fatal(text)
	}
	if !strings.Contains(evidenceFooter(c, nil, 1, 20), "Evidence:") {
		t.Fatal("English footer missing")
	}
	c.Report.SuggestActions = false
	if !strings.Contains(reportInstructions(c), "Do not add an action proposal section") {
		t.Fatal("action policy cannot be disabled")
	}
	c.Report.Language = "fr"
	if err := c.Validate(); err == nil {
		t.Fatal("unknown language accepted")
	}
}
func TestDiscordReceiptValidation(t *testing.T) {
	for _, body := range []string{`{"id":"12345","channel_id":"67890"}`, `{"id":"private-error","channel_id":"67890"}`, `{}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		client := http.Client{Transport: discordReceiptTransport{base: http.DefaultTransport}}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
		response, err := client.Do(req)
		if response != nil {
			response.Body.Close()
		}
		server.Close()
		if strings.Contains(body, "12345") {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || strings.Contains(err.Error(), "private-error") {
			t.Fatal("invalid receipt accepted")
		}
	}
}
