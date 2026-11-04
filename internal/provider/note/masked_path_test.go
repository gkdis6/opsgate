package note

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestMaskedFilenameOracle(t *testing.T) {
	p := fixture(t, map[string]string{"password=hunter2.md": "private", "공개.md": "ordinary"})
	for _, sensitive := range []bool{false, true} {
		for _, query := range []string{"password=h", "password=hu", "password=x", "hunter2"} {
			out, err := p.search(context.Background(), tool.Caller{}, searchInput{Query: query, CaseSensitive: sensitive})
			if err != nil || out != "total matches: 0; files scanned: 1; stopped at limit: false\n" {
				t.Errorf("query %q sensitive %t: %q %v", query, sensitive, out, err)
			}
		}
	}
}

func TestMaskedDecodedLinkOracle(t *testing.T) {
	p := fixture(t, map[string]string{
		"password=hunter2.md": "",
		"matching.md":         "[x](password%3Dhunter2.md)",
		"wrong.md":            "[y](password%3Dwrong.md)",
	})
	for _, name := range []string{"matching", "wrong"} {
		out, err := p.links(context.Background(), tool.Caller{}, pathInput{Path: name})
		want := "outgoing:\npassword=" + redact.Mask + " -> unresolved\nbacklinks:\n"
		if err != nil || redact.String(out) != want {
			t.Errorf("links %s: %q %v", name, out, err)
		}
	}
}

func TestMaskedPathsHidden(t *testing.T) {
	const ordinary = "정리/📚 노트.md"
	hidden := []string{"password=hunter2.md", "password=hunter2/nested.md", "ghp_abcdefgh.md"}
	files := map[string]string{ordinary: "#공개\nordinary\n"}
	for _, name := range hidden {
		files[name] = "#private [[정리/📚 노트]]"
	}
	p := fixture(t, files)
	if err := p.root.Symlink("password=hunter2", "alias"); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(hidden, "alias/nested.md", "password=wrong.md", "missing.md") {
		out, err := p.read(context.Background(), tool.Caller{}, readInput{Path: name})
		if out != "" || !errors.Is(err, errMissing) {
			t.Errorf("read %q: %q %v", name, out, err)
		}
		out, err = p.links(context.Background(), tool.Caller{}, pathInput{Path: name})
		if out != "" || !errors.Is(err, errMissing) {
			t.Errorf("links %q: %q %v", name, out, err)
		}
	}
	for _, folder := range []string{"password=hunter2", "password=wrong", "alias", "missing"} {
		out, err := p.list(context.Background(), tool.Caller{}, listInput{Folder: folder, Recursive: true})
		if out != "" || !errors.Is(err, errMissing) {
			t.Errorf("list %q: %q %v", folder, out, err)
		}
	}
	for _, tc := range []struct {
		name string
		run  func() (string, error)
		want string
	}{
		{"list", func() (string, error) {
			return p.list(context.Background(), tool.Caller{}, listInput{Recursive: true})
		}, ordinary},
		{"recent", func() (string, error) {
			return p.recent(context.Background(), tool.Caller{}, recentInput{})
		}, ordinary},
		{"tags", func() (string, error) {
			return p.tags(context.Background(), tool.Caller{}, tagInput{})
		}, "#공개\t1 notes\n"},
		{"backlinks", func() (string, error) {
			return p.links(context.Background(), tool.Caller{}, pathInput{Path: ordinary})
		}, "outgoing:\nbacklinks:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.run()
			if err != nil || !strings.Contains(out, tc.want) {
				t.Fatalf("%q %v", out, err)
			}
			for _, hidden := range []string{"password", "hunter2", "ghp_", "alias", "private"} {
				if strings.Contains(out, hidden) {
					t.Errorf("hidden path contributed to output: %q", out)
				}
			}
		})
	}
	out, err := p.read(context.Background(), tool.Caller{}, readInput{Path: ordinary})
	if err != nil || !strings.Contains(out, "path: "+ordinary) || !strings.HasSuffix(out, "#공개\nordinary\n") {
		t.Fatalf("ordinary read: %q %v", out, err)
	}
	out, err = p.search(context.Background(), tool.Caller{}, searchInput{Query: "📚 노트"})
	if err != nil || !strings.HasPrefix(out, ordinary+":0:") || !strings.Contains(out, "total matches: 1; files scanned: 1;") {
		t.Fatalf("ordinary filename search: %q %v", out, err)
	}
}

func TestMaskedPathWrites(t *testing.T) {
	p := fixture(t, map[string]string{"password=hunter2.md": "before", "password=hunter2/note.md": "before"})
	if err := p.root.Symlink("password=hunter2", "alias"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"password=hunter2.md", "password=wrong.md", "password=hunter2/new.md", "alias/new.md"} {
		_, err := p.planCreate(context.Background(), tool.Caller{}, createInput{Path: name, Content: "after"})
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "cannot create") || !strings.Contains(err.Error(), "masked") {
			t.Errorf("create %q: %v", name, err)
		} else if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "wrong") {
			t.Errorf("create echoed masked value: %v", err)
		}
	}
	for _, name := range []string{"password=hunter2.md", "password=wrong.md", "alias/note.md", "missing.md"} {
		for _, action := range []string{"append", "replace", "trash"} {
			var err error
			switch action {
			case "append":
				_, err = p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: name, Content: "after"})
			case "replace":
				_, err = p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: name, OldText: "before", NewText: "after"})
			case "trash":
				_, err = p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: name})
			}
			if !errors.Is(err, errMissing) {
				t.Errorf("%s %q: %v", action, name, err)
			}
		}
	}
	if got := readFixture(t, p, "password=hunter2.md"); got != "before" {
		t.Fatal("masked note changed")
	}
	for _, name := range []string{"password=wrong.md", "password=hunter2/new.md"} {
		if _, err := p.root.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("masked path created: %v", err)
		}
	}
}
