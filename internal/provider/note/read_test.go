package note

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestSearch(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   searchInput
		want []string
		bad  bool
	}{
		{"literal", searchInput{Query: "a.*b"}, []string{"notes/📚 한국어.md:5:", "total matches: 1"}, false},
		{"folded", searchInput{Query: "hello"}, []string{"total matches: 3"}, false},
		{"case sensitive", searchInput{Query: "HELLO", CaseSensitive: true}, []string{"total matches: 1"}, false},
		{"limit", searchInput{Query: "hello", Limit: 1}, []string{"total matches: 1", "stopped at limit: true"}, false},
		{"folder", searchInput{Query: "hello", Folder: "notes"}, []string{"total matches: 2", "files scanned: 1"}, false},
		{"filename", searchInput{Query: "한국어"}, []string{"notes/📚 한국어.md:0:"}, false},
		{"tag", searchInput{Query: "hello", Tag: "project"}, []string{"total matches: 2"}, false},
		{"empty", searchInput{}, nil, true}, {"large limit", searchInput{Query: "x", Limit: 101}, nil, true}, {"negative", searchInput{Query: "x", Limit: -1}, nil, true}, {"bad folder", searchInput{Query: "x", Folder: "../"}, nil, true}, {"bad tag", searchInput{Query: "x", Tag: "tag space"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t, map[string]string{"notes/📚 한국어.md": "---\ntags: [project/active]\n---\nHello HELLO\na.*b\n", "other.md": "hello", ".private/no.md": "hello", "conflict.sync-conflict-x.md": "hello", "big.md": strings.Repeat("hello", 1000)})
			p.cfg.MaxNoteBytes = 1000
			out, err := p.search(context.Background(), tool.Caller{}, tc.in)
			if (err != nil) != tc.bad {
				t.Fatalf("out=%q err=%v", out, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %s", want, out)
				}
			}
		})
	}
	for _, s := range []string{strings.Repeat("가", 300) + "needle" + strings.Repeat("📚", 300), strings.Repeat("x", 500)} {
		got := snippet(s, "needle", true)
		if utf8.RuneCountInString(got) > 200 || !utf8.ValidString(got) {
			t.Fatal(got)
		}
		if strings.Contains(s, "needle") && !strings.Contains(got, "needle") {
			t.Fatal("snippet missed hit")
		}
	}
}

func TestReadLines(t *testing.T) {
	p := fixture(t, map[string]string{"한글📚.md": "one\ntwo\nthree\n", "empty.md": ""})
	for _, tc := range []struct {
		in   readInput
		want string
		bad  bool
	}{
		{readInput{Path: "한글📚", OffsetLine: 2, MaxLines: 1}, "lines: 2-2 (total 3)\n\ntwo", false},
		{readInput{Path: "한글📚.md"}, "one\ntwo\nthree", false},
		{readInput{Path: "한글📚", OffsetLine: 4}, "lines: none (total 3)", false},
		{readInput{Path: "empty"}, "total 0", false},
		{readInput{Path: "한글📚", OffsetLine: 5}, "", true}, {readInput{Path: "한글📚", OffsetLine: -1}, "", true}, {readInput{Path: "한글📚", MaxLines: 2001}, "", true},
	} {
		out, err := p.read(context.Background(), tool.Caller{}, tc.in)
		if (err != nil) != tc.bad || !strings.Contains(out, tc.want) {
			t.Fatalf("%+v: %q %v", tc.in, out, err)
		}
	}
}

func TestListRecent(t *testing.T) {
	p := fixture(t, map[string]string{"a.md": "a", "old.md": "old", "z/nested.md": "n", "canvas.canvas": "{}", "base.base": "{}", "image.png": "no", ".hidden/no.md": "no"})
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(p.cfg.Vault, "old.md"), old, old); err != nil {
		t.Fatal(err)
	}
	out, err := p.list(context.Background(), tool.Caller{}, listInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "folder z/\n") || !strings.Contains(out, "canvas.canvas") || !strings.Contains(out, "base.base") || strings.Contains(out, "nested.md") || strings.Contains(out, "image.png") {
		t.Fatal(out)
	}
	out, err = p.list(context.Background(), tool.Caller{}, listInput{Recursive: true, Limit: 1})
	if err != nil || !strings.Contains(out, "truncated:") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = p.list(context.Background(), tool.Caller{}, listInput{Folder: "z", Recursive: true})
	if err != nil || !strings.Contains(out, "z/nested.md") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = p.recent(context.Background(), tool.Caller{}, recentInput{})
	if err != nil || strings.Contains(out, "old.md") || strings.Contains(out, "canvas") || !strings.Contains(out, "a.md") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = p.recent(context.Background(), tool.Caller{}, recentInput{Days: 31, Limit: 1})
	if err != nil || !strings.Contains(out, "truncated") {
		t.Fatalf("%s %v", out, err)
	}
	for _, in := range []recentInput{{Days: 366}, {Days: -1}, {Limit: 101}} {
		if _, err := p.recent(context.Background(), tool.Caller{}, in); err == nil {
			t.Fatal(in)
		}
	}
	if _, err := p.list(context.Background(), tool.Caller{}, listInput{Limit: 1001}); err == nil {
		t.Fatal("large list")
	}
}

func TestTags(t *testing.T) {
	for _, tc := range []struct {
		text   string
		want   []string
		absent []string
	}{
		{"---\ntags: [project/active, 한글, '#emoji']\n---\n# heading #ignored\n#inline #project/active\n```go\n#code\n```\n~~~\n#also-code\n~~~\n`#code` #real\n    #indented\n\t#tab-code", []string{"project/active", "한글", "emoji", "inline", "real"}, []string{"heading", "ignored", "code", "also-code", "indented", "tab-code"}},
		{"---\ntags: alpha, beta gamma/child\n---\n#extra", []string{"alpha", "beta", "gamma/child", "extra"}, nil},
		{"---\ntags:\n  - one\n  - two\n---\nword#not #yes", []string{"one", "two", "yes"}, []string{"not"}},
		{"---\ntags: &alias [foo]\nother: *alias\n---", []string{"foo"}, nil},
		{"issue #1036 and #2025/Q3 and color #007bff", []string{"2025/Q3", "007bff"}, []string{"1036"}},
	} {
		tags := extractTags(tc.text)
		for _, want := range tc.want {
			if !tags[want] {
				t.Errorf("missing %s: %v", want, tags)
			}
		}
		for _, absent := range tc.absent {
			if tags[absent] {
				t.Errorf("unexpected %s: %v", absent, tags)
			}
		}
	}
	p := fixture(t, map[string]string{"a.md": "---\ntags: project/active\n---\n#project/active #common", "b.md": "#common #project"})
	out, err := p.tags(context.Background(), tool.Caller{}, tagInput{})
	if err != nil || !strings.Contains(out, "#common\t2 notes") || !strings.Contains(out, "#project/active\t1 notes") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = p.tags(context.Background(), tool.Caller{}, tagInput{Tag: "#project"})
	if err != nil || !strings.Contains(out, "a.md") || !strings.Contains(out, "b.md") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestLinks(t *testing.T) {
	p := fixture(t, map[string]string{
		"folder/대상📚.md":   "[[other|alias]] ![[root#heading]] [relative](../root.md#x) [[missing]] [[dup]] [[.hidden/private]] [empty]()",
		"folder/other.md": "[[대상📚#heading|alias]]",
		"root.md":         "![[folder/대상📚|Alias]] [target](folder/%EB%8C%80%EC%83%81%F0%9F%93%9A.md#x)\n```\n[[fake]]\n```",
		"another.md":      "[[대상📚]]",
		"a/dup.md":        "", "b/dup.md": "", ".hidden/private.md": "[[대상📚]]",
	})
	out, err := p.links(context.Background(), tool.Caller{}, pathInput{Path: "folder/대상📚"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"other -> folder/other.md", "root -> root.md", "../root.md -> root.md", "missing -> unresolved", "dup -> unresolved", ".hidden/private -> unresolved", "backlinks:\nanother.md\nfolder/other.md\nroot.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	out, err = p.links(context.Background(), tool.Caller{}, pathInput{Path: "root"})
	if err != nil || !strings.Contains(out, "folder/대상📚 -> folder/대상📚.md") || strings.Contains(out, "fake") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestCancellationAndSize(t *testing.T) {
	p := fixture(t, map[string]string{"large.md": strings.Repeat("x", 101), "ok.md": "x"})
	p.cfg.MaxNoteBytes = 100
	if _, err := p.read(context.Background(), tool.Caller{}, readInput{Path: "large"}); err == nil {
		t.Fatal("oversize read")
	}
	out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: "x"})
	if err != nil || strings.Contains(out, "large.md:") {
		t.Fatalf("%s %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, run := range []func() (string, error){func() (string, error) { return p.search(ctx, tool.Caller{}, searchInput{Query: "x"}) }, func() (string, error) { return p.read(ctx, tool.Caller{}, readInput{Path: "ok"}) }, func() (string, error) { return p.list(ctx, tool.Caller{}, listInput{}) }, func() (string, error) { return p.tags(ctx, tool.Caller{}, tagInput{}) }, func() (string, error) { return p.recent(ctx, tool.Caller{}, recentInput{}) }, func() (string, error) { return p.links(ctx, tool.Caller{}, pathInput{Path: "ok"}) }} {
		if _, err := run(); err == nil {
			t.Fatal("canceled scan succeeded")
		}
	}
}

func TestSnippetsFollowEachHit(t *testing.T) {
	p := fixture(t, map[string]string{"note.md": "FIRST needle " + strings.Repeat("가", 500) + " LAST needle"})
	out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "FIRST needle") || !strings.Contains(lines[1], "LAST needle") {
		t.Fatal(out)
	}
}

func TestReadPreservesLineEndings(t *testing.T) {
	for _, text := range []string{"one\ntwo\n", "one\r\ntwo\r\n", "one\ntwo"} {
		p := fixture(t, map[string]string{"note.md": text})
		out, err := p.read(context.Background(), tool.Caller{}, readInput{Path: "note"})
		if err != nil {
			t.Fatal(err)
		}
		_, body, ok := strings.Cut(out, "\n\n")
		if !ok || body != text {
			t.Fatalf("got %q want %q", body, text)
		}
	}
}

func TestReadRedactsPEMPages(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		text := strings.Join([]string{"before", "-----BEGIN PRIVATE KEY-----", "abc", "def", "-----END PRIVATE KEY-----", "after", ""}, newline)
		p := fixture(t, map[string]string{"note.md": text})
		for _, tc := range []struct {
			offset, max int
			want        string
		}{
			{3, 1, redact.Mask + newline},
			{3, 2, redact.Mask + newline + redact.Mask + newline},
			{2, 5, strings.Repeat(redact.Mask+newline, 4) + "after" + newline},
			{6, 1, "after" + newline},
		} {
			out, err := p.read(context.Background(), tool.Caller{}, readInput{Path: "note", OffsetLine: tc.offset, MaxLines: tc.max})
			if err != nil {
				t.Fatal(err)
			}
			header, body, ok := strings.Cut(out, "\n\n")
			lineRange := fmt.Sprintf("lines: %d-%d (total 6)", tc.offset, tc.offset+tc.max-1)
			if !ok || body != tc.want || !strings.Contains(header, lineRange) {
				t.Fatalf("read %+v: %q", tc, out)
			}
		}
	}
}

func TestSearchRedactsSecretSnippets(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"token", "ghp_" + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250)},
		{"invalid UTF-8 prefix", strings.Repeat("\xff", 250) + " password=" + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250)},
		{"JWT", "eyJhbGci." + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250) + ".signature"},
		{"authorization", "Authorization: Digest " + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250)},
		{"assignment", "password=" + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250)},
		{"PEM", "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("a", 250) + "needle" + strings.Repeat("b", 250) + "\n-----END PRIVATE KEY-----"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t, map[string]string{"note.md": tc.text})
			out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: "needle"})
			if err != nil {
				t.Fatal(err)
			}
			want := "total matches: 0; files scanned: 1; stopped at limit: false\n"
			if out != want {
				t.Fatalf("search = %q, want %q", out, want)
			}
			if tc.name != "PEM" {
				if got := snippet(tc.text, "needle", true); got != redact.Mask {
					t.Fatalf("snippet = %q", got)
				}
			}
		})
	}
}
