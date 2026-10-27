package note

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestTrashReplacementAtMove(t *testing.T) {
	for _, content := range []string{"latest save", "approved"} {
		t.Run(content, func(t *testing.T) {
			p := fixture(t, map[string]string{"folder/note.md": "approved", ".trash/note.md": "existing"})
			plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "folder/note"})
			if err != nil {
				t.Fatal(err)
			}
			d := plan.Data.(writePlan)
			p.beforeTrashMove = func() {
				if err := p.root.WriteFile("folder/temp", []byte(content), 0664); err != nil {
					t.Fatal(err)
				}
				if err := p.root.Chtimes("folder/temp", d.mtime, d.mtime); err != nil {
					t.Fatal(err)
				}
				if err := p.root.Rename("folder/temp", "folder/note.md"); err != nil {
					t.Fatal(err)
				}
			}
			_, err = p.apply(context.Background(), plan)
			if err == nil || !strings.Contains(err.Error(), "note changed since it was approved") || !strings.Contains(err.Error(), ".trash/note.1.md") {
				t.Fatalf("missing change and recovery path: %v", err)
			}
			if got := readFixture(t, p, ".trash/note.1.md"); got != content {
				t.Fatalf("lost replacement: got %q want %q", got, content)
			}
			if got := readFixture(t, p, ".trash/note.md"); got != "existing" {
				t.Fatalf("overwrote existing trash: %q", got)
			}
			if _, err := p.root.Lstat("folder/note.md"); !os.IsNotExist(err) {
				t.Fatalf("source remains: %v", err)
			}
		})
	}
}

func TestTrashEditAtMove(t *testing.T) {
	for _, change := range []string{"content", "mtime", "oversize"} {
		t.Run(change, func(t *testing.T) {
			p := fixture(t, map[string]string{"note.md": "approved"})
			p.cfg.MaxNoteBytes = 16
			plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
			if err != nil {
				t.Fatal(err)
			}
			d := plan.Data.(writePlan)
			want := "approved"
			p.beforeTrashMove = func() {
				if change == "mtime" {
					if err := p.root.Chtimes("note.md", d.mtime, d.mtime.Add(time.Second)); err != nil {
						t.Fatal(err)
					}
					return
				}
				want = "modified"
				if change == "oversize" {
					want = strings.Repeat("x", p.cfg.MaxNoteBytes+1)
				}
				if err := p.root.WriteFile("note.md", []byte(want), 0664); err != nil {
					t.Fatal(err)
				}
				if err := p.root.Chtimes("note.md", d.mtime, d.mtime); err != nil {
					t.Fatal(err)
				}
			}
			_, err = p.apply(context.Background(), plan)
			if err == nil || !strings.Contains(err.Error(), "note changed since it was approved") || !strings.Contains(err.Error(), ".trash/note.md") {
				t.Fatalf("missing change and recovery path: %v", err)
			}
			if got := readFixture(t, p, ".trash/note.md"); got != want {
				t.Fatalf("lost edit: got %q want %q", got, want)
			}
		})
	}
}

func TestTrashDestinationAtMove(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			p := fixture(t, map[string]string{"note.md": "approved", "other.md": "other"})
			plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
			if err != nil {
				t.Fatal(err)
			}
			p.beforeTrashMove = func() {
				var err error
				if symlink {
					err = p.root.Symlink("../other.md", ".trash/note.md")
				} else {
					err = p.root.WriteFile(".trash/note.md", []byte("racing trash"), 0664)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "trash destination already exists") {
				t.Fatalf("accepted racing trash destination: %v", err)
			}
			if got := readFixture(t, p, "note.md"); got != "approved" {
				t.Fatalf("source changed: %q", got)
			}
			if symlink {
				if target, err := p.root.Readlink(".trash/note.md"); err != nil || target != "../other.md" {
					t.Fatalf("trash symlink changed: %q %v", target, err)
				}
			} else if got := readFixture(t, p, ".trash/note.md"); got != "racing trash" {
				t.Fatalf("overwrote trash: %q", got)
			}
			if got := readFixture(t, p, "other.md"); got != "other" {
				t.Fatalf("symlink target changed: %q", got)
			}
		})
	}
}

func TestTrashSymlinkAtMove(t *testing.T) {
	p := fixture(t, map[string]string{"note.md": "approved", "other.md": "other"})
	plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
	if err != nil {
		t.Fatal(err)
	}
	p.beforeTrashMove = func() {
		if err := p.root.Remove("note.md"); err != nil {
			t.Fatal(err)
		}
		if err := p.root.Symlink("other.md", "note.md"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = p.apply(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "note changed since it was approved") || !strings.Contains(err.Error(), ".trash/note.md") {
		t.Fatalf("missing change and recovery path: %v", err)
	}
	if target, err := p.root.Readlink(".trash/note.md"); err != nil || target != "other.md" {
		t.Fatalf("lost replacement symlink: %q %v", target, err)
	}
	if got := readFixture(t, p, "other.md"); got != "other" {
		t.Fatalf("symlink target changed: %q", got)
	}
}
