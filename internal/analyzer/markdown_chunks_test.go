package analyzer

import (
	"strings"
	"testing"
)

func TestMarkdownNotificationBoundaries(t *testing.T) {
	link := "[이미지 복구 승인](https://mcp.example.com/approve/restore-image/default/apps/web?container=app&expected_image=bad&image=good)"
	code := "```sh\nkubectl --context='<검증된_CONTEXT>' -n apps rollout status deployment/web\nkubectl -n apps get pods -o wide\n```\n"
	text := strings.Repeat("확인된 사실과 근거입니다. ", 70) + "\n\n권장 조치\n" + link + "\n\n" + code + "\n추가 확인 사항\n" + strings.Repeat("내용입니다. ", 120)
	parts := chunks(text, 1900)
	if strings.Join(parts, "") != text {
		t.Fatal("report content changed")
	}
	links, codes := 0, 0
	for _, p := range parts {
		if units(p) > 1900 {
			t.Fatal("Discord limit exceeded")
		}
		if strings.Contains(p, link) {
			links++
		}
		if strings.Contains(p, code) {
			codes++
		}
		if strings.Count(p, "```")%2 != 0 {
			t.Fatal("unbalanced code fence")
		}
	}
	if links != 1 || codes != 1 {
		t.Fatal("link or code block was split")
	}
}

func TestOversizedMarkdownParagraphKeepsLink(t *testing.T) {
	link := "[이미지 복구 승인](https://mcp.example.com/approve/restore-image/default/apps/web?container=app&expected_image=bad&image=good)"
	text := strings.Repeat("가", 1890) + link + strings.Repeat(" 설명", 900)
	parts := chunks(text, 1900)
	if strings.Join(parts, "") != text {
		t.Fatal("content lost")
	}
	found := false
	for _, p := range parts {
		if units(p) > 1900 {
			t.Fatal("limit exceeded")
		}
		found = found || strings.Contains(p, link)
	}
	if !found {
		t.Fatal("Markdown link was split")
	}
}

func TestOversizedCodeContinuesWithBalancedFences(t *testing.T) {
	line := "kubectl -n apps get deployment/web\n"
	text := "```sh\n" + strings.Repeat(line, 100) + "```\n"
	parts := chunks(text, 1900)
	lines := 0
	for _, p := range parts {
		if units(p) > 1900 || strings.Count(p, "```")%2 != 0 {
			t.Fatal("invalid code continuation")
		}
		lines += strings.Count(p, strings.TrimSpace(line))
	}
	if lines != 100 {
		t.Fatal("code lines lost")
	}
}
