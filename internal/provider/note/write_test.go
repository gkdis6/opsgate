package note

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
)

func TestWritePlansAndApply(t *testing.T) {
	for _, tc := range []struct {
		name, before, want string
		plan               func(*provider) (tool.Plan, error)
		summary            []string
	}{
		{"create", "", "new\n", func(p *provider) (tool.Plan, error) {
			return p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "new/folder/📚", Content: "new\n"})
		}, []string{"create note new/folder/📚.md", "current sha256: absent", "new content:\nnew\n"}},
		{"append", "before\n", "before\nnew\n", func(p *provider) (tool.Plan, error) {
			return p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: "new"})
		}, []string{"append note note.md", "current sha256:", "+new", "--- note.md"}},
		{"replace", "before\n", "after\n", func(p *provider) (tool.Plan, error) {
			return p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "before", NewText: "after"})
		}, []string{"replace note note.md", "-before", "+after", "@@"}},
		{"trash", "before\n", "before\n", func(p *provider) (tool.Plan, error) {
			return p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
		}, []string{"trash note note.md", "destination: .trash/note.md", "-before", "retain in local trash", "check the error for the recovery location"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t, map[string]string{"note.md": tc.before})
			beforeInfo, err := p.root.Stat("note.md")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := tc.plan(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.summary {
				if !strings.Contains(plan.Summary, want) {
					t.Fatalf("missing %q: %s", want, plan.Summary)
				}
			}
			if tc.name != "create" {
				hash := sha256.Sum256([]byte(tc.before))
				if !strings.Contains(plan.Summary, fmt.Sprintf("%x", hash[:8])) {
					t.Fatal(plan.Summary)
				}
			}
			// Planning must not create parents, trash or mutate the source.
			if _, err := p.root.Stat("new"); !os.IsNotExist(err) {
				t.Fatalf("plan created directory: %v", err)
			}
			if _, err := p.root.Stat(".trash"); !os.IsNotExist(err) {
				t.Fatalf("plan created trash: %v", err)
			}
			afterInfo, err := p.root.Stat("note.md")
			if err != nil || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) || readFixture(t, p, "note.md") != tc.before {
				t.Fatal("plan mutated source")
			}
			if _, err := p.apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			name := "note.md"
			if tc.name == "create" {
				name = "new/folder/📚.md"
			}
			if tc.name == "trash" {
				name = ".trash/note.md"
				if _, err := p.root.Stat("note.md"); !os.IsNotExist(err) {
					t.Fatal("source remains")
				}
			}
			if got := readFixture(t, p, name); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if tc.name == "create" {
				st, err := p.root.Stat(name)
				if err != nil || st.Mode().Perm() != 0664 {
					t.Fatalf("mode: %v %v", st, err)
				}
			}
			entries, err := p.root.Open(".")
			if err != nil {
				t.Fatal(err)
			}
			defer entries.Close()
			names, err := entries.Readdirnames(-1)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range names {
				if strings.HasPrefix(n, ".opsgate-") {
					t.Fatal("temp file remains")
				}
			}
		})
	}
}

func TestTOCTOURefusal(t *testing.T) {
	for _, action := range []string{"append", "replace", "trash"} {
		for _, change := range []string{"content", "size", "mtime", "inode"} {
			t.Run(action+"/"+change, func(t *testing.T) {
				p := fixture(t, map[string]string{"note.md": "before\n"})
				var plan tool.Plan
				var err error
				switch action {
				case "append":
					plan, err = p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: "new"})
				case "replace":
					plan, err = p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "before", NewText: "after"})
				case "trash":
					plan, err = p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
				}
				if err != nil {
					t.Fatal(err)
				}
				d := plan.Data.(writePlan)
				switch change {
				case "content":
					if err := p.root.WriteFile("note.md", []byte("change\n"), 0664); err != nil {
						t.Fatal(err)
					}
					if err := p.root.Chtimes("note.md", d.mtime, d.mtime); err != nil {
						t.Fatal(err)
					}
				case "size":
					if err := p.root.WriteFile("note.md", []byte("much longer now"), 0664); err != nil {
						t.Fatal(err)
					}
				case "mtime":
					if err := p.root.Chtimes("note.md", time.Now(), d.mtime.Add(time.Second)); err != nil {
						t.Fatal(err)
					}
				case "inode":
					if err := p.root.Rename("note.md", "old.md"); err != nil {
						t.Fatal(err)
					}
					if err := p.root.WriteFile("note.md", []byte("before\n"), 0664); err != nil {
						t.Fatal(err)
					}
					if err := p.root.Chtimes("note.md", d.mtime, d.mtime); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := p.apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "note changed since it was approved") {
					t.Fatal(err)
				}
				if _, err := p.root.Stat(".trash"); !os.IsNotExist(err) {
					t.Fatal("failed apply created trash")
				}
			})
		}
	}
}

func TestAppendHeadingAndEnding(t *testing.T) {
	for _, tc := range []struct {
		name, before, content, heading, want string
		bad                                  bool
	}{
		{"end no newline", "old", "new", "", "old\nnew", false},
		{"end newline", "old\n", "new\n", "", "old\nnew\n", false},
		{"CRLF", "old\r\n", "new", "", "old\r\nnew\r\n", false},
		{"section", "# One\nbody\n## Child\nchild\n# Two\nend\n", "added", "One", "# One\nbody\n## Child\nchild\nadded\n# Two\nend\n", false},
		{"nested", "# One\n## Child\nchild\n## Other\nend", "added", "Child", "# One\n## Child\nchild\nadded\n## Other\nend", false},
		{"heading closing hashes", "## One ##\nbody\n", "added", "One", "## One ##\nbody\nadded\n", false},
		{"code ignored", "```\n# One\n```\n# One\nbody\n", "added", "One", "```\n# One\n```\n# One\nbody\nadded\n", false},
		{"missing", "# One\nbody", "added", "Two", "", true},
		{"ambiguous", "# One\n## One\nbody", "added", "One", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture(t, map[string]string{"note.md": tc.before})
			plan, err := p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: tc.content, Heading: tc.heading})
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if tc.bad {
				return
			}
			if _, err := p.apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, p, "note.md"); got != tc.want {
				t.Fatalf("%q != %q", got, tc.want)
			}
		})
	}
}

func TestReplaceCountAndEnding(t *testing.T) {
	for _, tc := range []struct {
		before string
		in     replaceInput
		want   string
		bad    bool
	}{
		{"x x\n", replaceInput{OldText: "x", NewText: "y"}, "", true},
		{"x x\n", replaceInput{OldText: "x", NewText: "y", ExpectedCount: 2}, "y y\n", false},
		{"x", replaceInput{OldText: "x", NewText: "y\n"}, "y", false},
		{"x\n", replaceInput{OldText: "x\n", NewText: "y"}, "y\n", false},
		{"x\r\n", replaceInput{OldText: "x", NewText: "y"}, "y\r\n", false},
		{"x", replaceInput{OldText: "", NewText: "y"}, "", true},
		{"x", replaceInput{OldText: "x", NewText: "y", ExpectedCount: -1}, "", true},
	} {
		p := fixture(t, map[string]string{"note.md": tc.before})
		tc.in.Path = "note"
		plan, err := p.planReplace(context.Background(), tool.Caller{}, tc.in)
		if (err != nil) != tc.bad {
			t.Fatal(err)
		}
		if tc.bad {
			continue
		}
		if _, err := p.apply(context.Background(), plan); err != nil {
			t.Fatal(err)
		}
		if got := readFixture(t, p, "note.md"); got != tc.want {
			t.Fatalf("%q != %q", got, tc.want)
		}
	}
}

func TestCreateRace(t *testing.T) {
	p := fixture(t, nil)
	plan, err := p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "note", Content: "approved"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.root.WriteFile("note.md", []byte("racer"), 0664); err != nil {
		t.Fatal(err)
	}
	if _, err := p.apply(context.Background(), plan); err == nil {
		t.Fatal("overwrote racing creator")
	}
	if got := readFixture(t, p, "note.md"); got != "racer" {
		t.Fatal(got)
	}
	// Race at publication itself, independently of Plan and the provider mutex.
	for round := 0; round < 10; round++ {
		name := fmt.Sprintf("race-%d.md", round)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, content := range []string{"first", "second"} {
			wg.Add(1)
			go func(text string) {
				defer wg.Done()
				<-start
				results <- atomicWrite(context.Background(), p.root, name, text, 0664, true)
			}(content)
		}
		close(start)
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("%d successful racing creates", successes)
		}
		if got := readFixture(t, p, name); got != "first" && got != "second" {
			t.Fatal(got)
		}
	}
}

func TestTrashNamingAndRace(t *testing.T) {
	p := fixture(t, map[string]string{"folder/note.md": "source", ".trash/note.md": "existing", ".trash/note.1.md": "existing 1"})
	plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "folder/note"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Summary, ".trash/note.2.md") {
		t.Fatal(plan.Summary)
	}
	if _, err := p.apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, p, ".trash/note.2.md"); got != "source" {
		t.Fatal(got)
	}
	put(t, p.cfg.Vault, "folder/note.md", "new")
	plan, err = p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "folder/note"})
	if err != nil {
		t.Fatal(err)
	}
	put(t, p.cfg.Vault, ".trash/note.3.md", "racing trash")
	if _, err := p.apply(context.Background(), plan); err == nil {
		t.Fatal("overwrote trash")
	}
	if got := readFixture(t, p, "folder/note.md"); got != "new" {
		t.Fatal(got)
	}
	if got := readFixture(t, p, ".trash/note.3.md"); got != "racing trash" {
		t.Fatal(got)
	}
	out, err := p.list(context.Background(), tool.Caller{}, listInput{Recursive: true})
	if err != nil || strings.Contains(out, ".trash") {
		t.Fatalf("%s %v", out, err)
	}
}

func TestTrashReplacementAfterApproval(t *testing.T) {
	p := fixture(t, map[string]string{"note.md": "approved"})
	plan, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.root.WriteFile("temp", []byte("latest save"), 0664); err != nil {
		t.Fatal(err)
	}
	if err := p.root.Rename("temp", "note.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.apply(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "note changed since it was approved") {
		t.Fatal(err)
	}
	if got := readFixture(t, p, "note.md"); got != "latest save" {
		t.Fatalf("lost replacement: %q", got)
	}
	if _, err := p.root.Lstat(".trash"); !os.IsNotExist(err) {
		t.Fatalf("failed apply created trash: %v", err)
	}
}

func TestTrashSymlink(t *testing.T) {
	p := fixture(t, map[string]string{"note.md": "note", "elsewhere/existing.md": "other"})
	if err := p.root.Symlink("elsewhere", ".trash"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "note"}); err == nil {
		t.Fatal("symlinked trash accepted")
	}
}

func TestWriteLimitsAndPreview(t *testing.T) {
	p := fixture(t, map[string]string{"note.md": "12345\n", "huge.md": strings.Repeat("x", 101)})
	p.cfg.MaxNoteBytes = 100
	for _, plan := range []func() (tool.Plan, error){
		func() (tool.Plan, error) {
			return p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "big", Content: strings.Repeat("x", 101)})
		},
		func() (tool.Plan, error) {
			return p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: strings.Repeat("x", 101)})
		},
		func() (tool.Plan, error) {
			return p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "1", NewText: strings.Repeat("x", 101)})
		},
		func() (tool.Plan, error) {
			return p.planTrash(context.Background(), tool.Caller{}, trashInput{Path: "huge"})
		},
		func() (tool.Plan, error) {
			return p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "note", Content: "x"})
		},
	} {
		if _, err := plan(); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	p.cfg.MaxNoteBytes = 1 << 20
	for _, action := range []string{"create", "append", "replace"} {
		over := strings.Repeat("x", maxContentBytes+1)
		var err error
		switch action {
		case "create":
			_, err = p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "new", Content: over})
		case "append":
			_, err = p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: over})
		case "replace":
			_, err = p.planReplace(context.Background(), tool.Caller{}, replaceInput{Path: "note", OldText: "1", NewText: over})
		}
		if err == nil {
			t.Fatal(action)
		}
	}
	large := strings.Repeat("한글 line\n", 10000)
	plan, err := p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "new", Content: large})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Summary) > 8<<10 || !strings.Contains(plan.Summary, "… ") || !strings.Contains(plan.Summary, " more lines") {
		t.Fatalf("length=%d summary=%s", len(plan.Summary), plan.Summary)
	}
	plan, err = p.planAppend(context.Background(), tool.Caller{}, appendInput{Path: "note", Content: "new"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.apply(ctx, plan); err == nil {
		t.Fatal("canceled write")
	}
	p.cfg.AllowWrite = false
	if _, err := p.apply(context.Background(), plan); err == nil {
		t.Fatal("disabled write")
	}
	if _, err := p.planCreate(context.Background(), tool.Caller{}, createInput{Path: "new", Content: "x"}); err == nil {
		t.Fatal("disabled create")
	}
}
