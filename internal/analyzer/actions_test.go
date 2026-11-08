package analyzer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func actionResult(v any) *mcp.CallToolResult {
	raw, _ := json.Marshal(v)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

func TestCatalogActionButtons(t *testing.T) {
	c := Config{}
	c.Report.ApprovalURL = "https://ops.example"
	c.Report.SuggestActions = true
	for _, language := range []string{"en", "ko"} {
		c.Report.Language = language
		for _, name := range []string{"k8s_rollout_restart", "k8s_restore_image", "k8s_scale", "k8s_delete_pod"} {
			entry, _ := action.Lookup(name)
			args := map[string]any{}
			for _, key := range entry.Arguments {
				args[key] = "value"
			}
			if name == "k8s_scale" {
				args["replicas"] = 2
			}
			link, err := action.URL(c.Report.ApprovalURL, name, args, "ai")
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []any{map[string]any{"url": link}, map[string]any{"actions": []any{map[string]string{"url": link}, map[string]string{"url": link}}}} {
				got := remediationLinks(c, actionResult(body))
				prefix := "AI suggestion: "
				if language == "ko" {
					prefix = "AI 제안: "
				}
				if len(got) != 1 || got[0].Label != prefix+entry.Label(language) {
					t.Fatalf("%s %s: %v", language, name, got)
				}
			}
		}
	}
	legacy := "https://ops.example/approve/restore-image/main/apps/web?container=app&expected_image=bad&image=good"
	got := remediationLinks(c, actionResult(map[string]any{"actions": []any{map[string]string{"url": legacy}}}))
	if len(got) != 1 || got[0].Label != "이미지 복구" {
		t.Fatalf("legacy alias: %v", got)
	}
}

func TestUntrustedActionURLs(t *testing.T) {
	c := Config{}
	c.Report.ApprovalURL = "https://ops.example"
	c.Report.SuggestActions = true
	base := "https://ops.example/approve/action/k8s_delete_pod?namespace=apps&name=web"
	for _, link := range []string{
		strings.Replace(base, "ops.example", "evil.example", 1), strings.Replace(base, "https:", "http:", 1), strings.Replace(base, "ops.example", "user@ops.example", 1),
		base + "#fragment", base + "&approval_id=forged", base + "&source=ai&source=rule", base + "&name=other", base + "&extra=bad",
		strings.Replace(base, "k8s_delete_pod", "note_trash", 1), strings.Replace(base, "/approve/action/", "/approve/other/", 1),
	} {
		if links := remediationLinks(c, actionResult(map[string]string{"url": link})); len(links) != 0 {
			t.Errorf("accepted %s", link)
		}
	}
	actions := []any{}
	for i := 0; i < 8; i++ {
		actions = append(actions, map[string]string{"url": base + strings.Repeat("a", i)})
	}
	if got := remediationLinks(c, actionResult(map[string]any{"actions": actions})); len(got) != 5 {
		t.Fatalf("cap: %d", len(got))
	}
}

func TestActionLinkPromptAndDefaultAllowlist(t *testing.T) {
	found := false
	for _, name := range readTools {
		found = found || name == "action_link"
	}
	if !found {
		t.Fatal("missing read tool")
	}
	c := Config{}
	c.Report.SuggestActions = true
	prompt := reportInstructions(c)
	for _, want := range []string{"only by calling action_link", "Every action requires a person", "least invasive"} {
		if !strings.Contains(prompt, want) {
			t.Fatal(want)
		}
	}
}

type linkSession struct{ fakeSession }

func (s *linkSession) List(ctx context.Context) ([]*mcp.Tool, error) {
	tools, err := s.fakeSession.List(ctx)
	return append(tools, &mcp.Tool{Name: "action_link", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}), err
}
func (s *linkSession) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if name == "action_link" {
		s.calls = append(s.calls, name)
		return actionResult(map[string]string{"url": "https://ops.example/approve/action/k8s_scale?cluster=main&kind=deployment&namespace=apps&name=web&replicas=2&source=ai"}), nil
	}
	return s.fakeSession.Call(ctx, name, args)
}

type linkModel struct{ turn int }

func (m *linkModel) Complete(context.Context, []message, []definition, int) (modelReply, error) {
	m.turn++
	if m.turn == 1 {
		return modelReply{Tokens: 1, Message: message{Calls: []toolCall{
			{ID: "get", Type: "function", Function: function{Name: "k8s_get", Arguments: `{"kind":"deployment","namespace":"apps","name":"web"}`}},
			{ID: "link", Type: "function", Function: function{Name: "action_link", Arguments: `{"tool":"k8s_scale","arguments":{"cluster":"main","kind":"deployment","namespace":"apps","name":"web","replicas":2}}`}},
		}}}, nil
	}
	return modelReply{Tokens: 1, Message: message{Content: "Review the proposed action [E2]."}}, nil
}

func TestInvestigationCollectsActionLinkResult(t *testing.T) {
	c := Default()
	c.Report.ApprovalURL = "https://ops.example"
	c.Report.SuggestActions = true
	c.Report.Language = "en"
	s := &linkSession{}
	e := engine{cfg: c, model: &linkModel{}, dial: func(context.Context) (toolSession, error) { return s, nil }}
	result, err := e.investigate(context.Background(), testBatch(time.Now()))
	if err != nil || len(result.Actions) != 1 || result.Actions[0].Label != "AI suggestion: Scale workload" {
		t.Fatalf("actions=%v error=%v", result.Actions, err)
	}
}

func TestRuleAndAIButtonsTogether(t *testing.T) {
	for _, language := range []string{"en", "ko"} {
		c := Default()
		c.Report.ApprovalURL = "https://ops.example"
		c.Report.Language = language
		args := map[string]any{"cluster": "main", "namespace": "apps", "kind": "deployment", "name": "web"}
		rule, _ := action.URL(c.Report.ApprovalURL, "k8s_rollout_restart", args, "rule")
		args["replicas"] = 2
		ai, _ := action.URL(c.Report.ApprovalURL, "k8s_scale", args, "ai")
		links := mergeLinks(remediationLinks(c, actionResult(map[string]any{"actions": []any{map[string]string{"url": rule, "label": "untrusted"}}})), remediationLinks(c, actionResult(map[string]string{"url": ai})))
		prefix := "AI suggestion: "
		if language == "ko" {
			prefix = "AI 제안: "
		}
		restart, _ := action.Lookup("k8s_rollout_restart")
		scale, _ := action.Lookup("k8s_scale")
		if len(links) != 2 || links[0].Label != restart.Label(language) || links[1].Label != prefix+scale.Label(language) {
			t.Fatal(links)
		}
		batch := testBatch(time.Now())
		batch.Alerts[0].OptionsURL = "https://evil.example/do-not-follow"
		report := reportNotification(c, batch, investigation{Text: "report"}, "completed", "")
		label := "All choices"
		if language == "ko" {
			label = "모든 선택지 보기"
		}
		if !strings.Contains(report, "["+label+"](https://ops.example/approve/options/main/apps/web)") || strings.Contains(report, "evil.example") {
			t.Fatal(report)
		}
	}
}

type mixedLinkSession struct{ linkSession }

func (s *mixedLinkSession) List(ctx context.Context) ([]*mcp.Tool, error) {
	tools, err := s.linkSession.List(ctx)
	return append(tools, &mcp.Tool{Name: "k8s_remediation_options", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}), err
}
func (s *mixedLinkSession) Call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if name == "k8s_remediation_options" {
		return actionResult(map[string]any{"actions": []any{map[string]string{"url": "https://ops.example/approve/action/k8s_rollout_restart?cluster=main&kind=deployment&namespace=apps&name=web&source=rule"}}}), nil
	}
	return s.linkSession.Call(ctx, name, args)
}
func TestInvestigationCollectsRulesAndAI(t *testing.T) {
	c := Default()
	c.Report.ApprovalURL = "https://ops.example"
	c.Report.Language = "en"
	s := &mixedLinkSession{}
	e := engine{cfg: c, model: &linkModel{}, dial: func(context.Context) (toolSession, error) { return s, nil }}
	result, err := e.investigate(context.Background(), testBatch(time.Now()))
	if err != nil || len(result.Actions) != 2 || result.Actions[0].Label != "Restart workload" || result.Actions[1].Label != "AI suggestion: Scale workload" {
		t.Fatalf("actions=%v err=%v", result.Actions, err)
	}
}
