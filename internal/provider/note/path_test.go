package note

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func fixture(t *testing.T, files map[string]string) *provider {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		put(t, dir, name, text)
	}
	cfg := config.Default().Providers.Note
	cfg.Vault = dir
	cfg.AllowWrite = true
	p, err := openVault(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.root.Close() })
	return p
}
func put(t *testing.T, root, name, text string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0775); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0664); err != nil {
		t.Fatal(err)
	}
}
func readFixture(t *testing.T, p *provider, name string) string {
	t.Helper()
	b, err := p.root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPathPolicy(t *testing.T) {
	p := fixture(t, map[string]string{"정리/📚 노트.md": "안녕하세요\n", "safe/note.md": "inside", ".obsidian/private.md": "hidden", "private/note.md": "private", "draft/note.md": "draft", "note.sync-conflict-copy.md": "conflict"})
	p.cfg.Exclude = []string{"private", "draft/*", "secret*"}
	outside := t.TempDir()
	put(t, outside, "outside.md", "outside")
	for name, target := range map[string]string{"outside-dir": outside, "outside.md": filepath.Join(outside, "outside.md"), "inside": "safe", "alias.md": "safe/note.md", "hidden": ".obsidian", "hidden-file.md": ".obsidian/private.md", "private-alias": "private", "loop": "loop"} {
		if err := p.root.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"../outside.md", "/etc/passwd", "safe//note.md", "safe/../safe/note.md", "safe/./note.md", "safe/", "", "\x00.md", "a\nb.md", "a\x7fb.md", "a\\b.md", strings.Repeat("a", 1025), ".obsidian/private.md", "private/note.md", "draft/note.md", "secret-dir/note.md", "note.sync-conflict-copy.md", "hidden/private.md", "hidden-file.md", "private-alias/note.md", "outside-dir/outside.md", "outside.md", "loop/note.md"} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.read(context.Background(), tool.Caller{}, readInput{Path: name}); err == nil {
				t.Fatalf("accepted %q", name)
			}
		})
	}
	for _, name := range []string{"정리/📚 노트", "safe/note.md", "inside/note.md", "alias.md"} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.read(context.Background(), tool.Caller{}, readInput{Path: name}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, name := range []string{".trash/a.md", ".git/a.md", ".stversions/a.md", ".stfolder/a.md", ".smart-env/a.md", ".space/a.md", ".makemd/a.md", ".claude/a.md", ".DS_Store"} {
		if err := p.validPath(name, false); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("excluded %q: %v", name, err)
		}
	}
	out, err := p.list(context.Background(), tool.Caller{}, listInput{Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"outside", "private", ".obsidian", "sync-conflict", "draft/note"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("leaked %q: %s", hidden, out)
		}
	}
}

func TestSymlinkRetargetAfterApproval(t *testing.T) {
	for _, action := range []string{"append", "create", "trash"} {
		t.Run(action, func(t *testing.T) {
			p := fixture(t, map[string]string{"safe/note.md": "old", ".hidden/note.md": "hidden"})
			if err := p.root.Symlink("safe", "alias"); err != nil {
				t.Fatal(err)
			}
			var plan tool.Plan
			var err error
			switch action {
			case "append":
				plan, err = p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "alias/note", Content: "new"})
			case "create":
				plan, err = p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "alias/new", Content: "new"})
			case "trash":
				plan, err = p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "alias/note"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := p.root.Remove("alias"); err != nil {
				t.Fatal(err)
			}
			if err := p.root.Symlink(".hidden", "alias"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "changed since") {
				t.Fatal(err)
			}
			if got := readFixture(t, p, ".hidden/note.md"); got != "hidden" {
				t.Fatal(got)
			}
		})
	}
}

func TestOpenVaultValidation(t *testing.T) {
	dir := t.TempDir()
	for _, cfg := range []config.Note{{Vault: "relative", MaxNoteBytes: 10}, {Vault: dir + "/missing", MaxNoteBytes: 10}, {Vault: dir, MaxNoteBytes: 0}, {Vault: dir, MaxNoteBytes: 10, Exclude: []string{"["}}, {Vault: dir, MaxNoteBytes: 10, Exclude: []string{"../secret"}}} {
		if p, err := openVault(cfg); err == nil {
			p.root.Close()
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestSpecialFileRefused(t *testing.T) {
	p := fixture(t, nil)
	// Directories named .md must never be treated as notes.
	if err := p.root.Mkdir("dir.md", 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := p.load("dir.md"); err == nil {
		t.Fatal("read directory as note")
	}
}

func TestScanSafeSymlinksAndCycles(t *testing.T) {
	p := fixture(t, map[string]string{"safe/note.md": "visible", ".hidden/note.md": "secret"})
	for name, target := range map[string]string{"alias": "safe", "safe/back": "..", "hidden-alias": ".hidden"} {
		if err := p.root.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.list(context.Background(), tool.Caller{}, listInput{Recursive: true})
	if err != nil || !strings.Contains(out, "alias/note.md") || strings.Contains(out, "hidden-alias") || strings.Contains(out, "back/safe/back") {
		t.Fatalf("%s %v", out, err)
	}
	out, err = p.search(context.Background(), tool.Caller{}, searchInput{Query: "visible", Folder: "alias"})
	if err != nil || !strings.Contains(out, "alias/note.md:1:") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestExcludedAliasesRemainMissingForCreate(t *testing.T) {
	p := fixture(t, map[string]string{".hidden/exists.md": "hidden"})
	if err := p.root.Symlink(".hidden", "alias"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alias/exists", "alias/missing", ".hidden/exists", ".hidden/missing"} {
		if _, err := p.planCreate(context.Background(), tool.Caller{}, createInput{Path: name, Content: "new"}); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUnicodePathsKeepBytes(t *testing.T) {
	p := fixture(t, map[string]string{"é.md": "composed", "e\u0301.md": "decomposed"})
	for _, tc := range []struct{ name, want string }{{"é", "composed"}, {"e\u0301", "decomposed"}} {
		out, err := p.read(context.Background(), tool.Caller{}, readInput{Path: tc.name})
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%q %v", out, err)
		}
	}
}
