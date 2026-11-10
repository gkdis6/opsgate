package analyzer

import (
	"context"
	"github.com/gkdis6/opsgate/internal/alert"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportLanguagesAndProposals(t *testing.T) {
	c := Default()
	ko := reportInstructions(c)
	for _, text := range []string{"Report in Korean", "**원인**", "**지금 할 일**", "**근거**", "own fenced code block", "placeholder requiring validation", "NEVER tool calls", "Do not recommend a restart"} {
		if !strings.Contains(ko, text) {
			t.Errorf("missing policy %q", text)
		}
	}
	// The footer names each read once and leaves its arguments out.
	footer := evidenceFooter(c, []string{`[E1] k8s_get {"name":"web","namespace":"apps"} (ok)`, `[E2] k8s_logs {"pod":"web"} (tool error)`}, 2, 20, 0, 0)
	if !strings.Contains(footer, "[E1] k8s_get · [E2] k8s_logs (실패)\n조회 2회") || strings.Contains(footer, "namespace") {
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
	if !strings.Contains(evidenceFooter(c, []string{"[E1] k8s_get {} (failed)"}, 1, 20, 0, 0), "[E1] k8s_get (failed)\n1 reads") {
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

func TestAllChoicesIgnoresUnsupportedHint(t *testing.T) {
	c := Default()
	c.Report.Language = "en"
	c.Report.ApprovalURL = "https://ops.example"
	b := testBatch(time.Now())
	b.Alerts[0].Workload = &alert.Workload{Kind: "job", Name: "job"}
	got := reportNotification(c, b, investigation{}, "failed", "timeout")
	if !strings.Contains(got, "[All choices](https://ops.example/approve/options/main/apps/web)") {
		t.Fatal(got)
	}
}
