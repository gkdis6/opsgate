package note

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestSearchSecretOracle(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		text := strings.Join([]string{"password=hunter2", "password=\"#hidden/child\"", "-----BEGIN PRIVATE KEY-----", "#pemtag [[pemlink]]", "pemneedle", "-----END PRIVATE KEY-----", "한글 İ 공개 공개 password=비밀", "#공개/태그", ""}, newline)
		p := fixture(t, map[string]string{"note.md": text})
		for _, sensitive := range []bool{false, true} {
			for _, query := range []string{"password=h", "password=hu", "password=x", "hunter2", "pemneedle", "pemtag", "비밀"} {
				out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: query, CaseSensitive: sensitive})
				if err != nil || out != "total matches: 0; files scanned: 1; stopped at limit: false\n" {
					t.Errorf("query %q sensitive %t: %q %v", query, sensitive, out, err)
				}
			}
		}
		for _, tag := range []string{"hidden", "hidden/child", "pemtag", "missing"} {
			out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: "공개", Tag: tag})
			if err != nil || out != "total matches: 0; files scanned: 1; stopped at limit: false\n" {
				t.Errorf("tag %q: %q %v", tag, out, err)
			}
		}
		out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: "공개", Tag: "공개", Limit: 2})
		want := "note.md:7: 한글 İ 공개 공개 password=" + redact.Mask + "\nnote.md:7: 한글 İ 공개 공개 password=" + redact.Mask + "\ntotal matches: 2; files scanned: 1; stopped at limit: true\n"
		if err != nil || out != want {
			t.Fatalf("ordinary search: %q %v", out, err)
		}
	}
}

func TestTagsAndLinksMaskSecrets(t *testing.T) {
	p := fixture(t, map[string]string{
		"note.md": "password=\"#hunter2\"\npassword=\"[[hunter2]]\"\npassword=\"[secret](hidden.md)\"\n-----BEGIN PRIVATE KEY-----\n#pemtag [[pemlink]]\n-----END PRIVATE KEY-----\n#공개 [[대상]] [public](대상.md)\n",
		"대상.md":   "password=\"[[note]]\"\npassword=\"[secret](note.md)\"\n-----BEGIN PRIVATE KEY-----\n[[note]]\n-----END PRIVATE KEY-----\n",
		"공개.md":   "[[note]]",
	})
	out, err := p.tags(context.Background(), tool.Caller{}, tagInput{})
	if err != nil || out != "#공개\t1 notes\n" {
		t.Errorf("tags: %q %v", out, err)
	}
	for _, tag := range []string{"hunter2", "pemtag", "missing"} {
		out, err := p.tags(context.Background(), tool.Caller{}, tagInput{Tag: tag})
		if err != nil || out != "" {
			t.Errorf("tag %q: %q %v", tag, out, err)
		}
	}
	out, err = p.tags(context.Background(), tool.Caller{}, tagInput{Tag: "공개"})
	if err != nil || out != "note.md\n" {
		t.Errorf("ordinary tag: %q %v", out, err)
	}
	out, err = p.links(context.Background(), tool.Caller{}, pathInput{Path: "note"})
	if err != nil || out != "outgoing:\n대상 -> 대상.md\n대상.md -> 대상.md\nbacklinks:\n공개.md\n" {
		t.Errorf("links: %q %v", out, err)
	}
}

func TestReplaceSecretOracle(t *testing.T) {
	text := "password=hunter2\npassword=\"한글비밀\"\n-----BEGIN PRIVATE KEY-----\npemneedle\n-----END PRIVATE KEY-----\n공개 hunter 공개\n"
	p := fixture(t, map[string]string{"note.md": text})
	for _, count := range []int{1, 2} {
		for _, old := range []string{"password=h", "password=hu", "password=x", "hunter2", "hunter2\npassword", "한글비밀", "pemneedle", "KEY-----\npem", "[REDACTED]"} {
			_, err := p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: old, NewText: "x", ExpectedCount: count})
			want := fmt.Sprintf("replace count mismatch: expected %d, found 0", count)
			if err == nil || err.Error() != want {
				t.Errorf("old %q count %d: %v", old, count, err)
			}
		}
	}
	_, err := p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "hunter", NewText: "x", ExpectedCount: 2})
	if err == nil || err.Error() != "replace count mismatch: expected 2, found 1" {
		t.Fatalf("mixed visible and hidden matches: %v", err)
	}
	plan, err := p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "hunter", NewText: "방문"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, p, "note.md"); got != strings.Replace(text, "공개 hunter 공개", "공개 방문 공개", 1) {
		t.Fatalf("secret changed: %q", got)
	}
}

func TestAppendSecretHeadingOracle(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		text := strings.Join([]string{"# password=hunter2", "-----BEGIN PRIVATE KEY-----", "# 비밀", "# 공개", "-----END PRIVATE KEY-----", "# 공개", "body", "# 끝", "end", ""}, newline)
		p := fixture(t, map[string]string{"note.md": text})
		for _, heading := range []string{"password=hunter2", "password=x", "password=[REDACTED]", "비밀", "missing"} {
			_, err := p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Heading: heading, Content: "added"})
			if err == nil || err.Error() != "heading absent" {
				t.Errorf("heading %q: %v", heading, err)
			}
		}
		plan, err := p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Heading: "공개", Content: "추가"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.apply(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		if got := readFixture(t, p, "note.md"); got != strings.Replace(text, "# 끝", "추가"+newline+"# 끝", 1) {
			t.Fatalf("secret or section changed: %q", got)
		}
	}
	p := fixture(t, map[string]string{"note.md": "# password=[REDACTED]\n"})
	_, err := p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Heading: "password=[REDACTED]", Content: "added"})
	if err == nil || err.Error() != "heading absent" {
		t.Fatalf("literal mask in a secret heading: %v", err)
	}
}
