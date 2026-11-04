package devrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
)

func TestDiffRedactsBeforeCut(t *testing.T) {
	_, repo := runnerConfig(t, "")
	r := ownershipRunner(t)
	r.cfg.WorkDir = r.root.Name()
	id := strings.Repeat("a", 20)
	worktree := filepath.Join(r.dir(id), "worktree")
	testGit(t, repo, "clone", repo, worktree)
	base := testGit(t, worktree, "rev-parse", "HEAD")
	r.tasks = []*Task{{ID: id, owner: "owner", State: "succeeded", BaseCommit: base}}
	for _, tc := range []struct{ name, source, want string }{
		{"quoted", `password="` + strings.Repeat("secret-value ", MaxReadBytes/13+1) + `"` + "\nafter\n", `+password="` + redact.Mask},
		{"PEM body", "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("QUJD", MaxReadBytes/4) + "\n-----END PRIVATE KEY-----\nafter\n", redact.Mask},
		{"PEM marker", "-----BEGIN PRIVATE KEY-----\nQUJD\n-----END PRIVATE KEY-----\nafter\n", redact.Mask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source
			if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "PEM marker" {
				out, _, err := git(context.Background(), worktree, maxLogBytes, "diff", "--no-ext-diff", "--no-textconv", "--stat", "--patch", base, "--")
				if err != nil {
					t.Fatal(err)
				}
				// Put the BEGIN marker itself across the response cut.
				padding := MaxReadBytes - strings.Index(out, "-----BEGIN") - 10
				source = strings.Repeat("x", padding) + source
				if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out := ownershipRequest(r, "GET", "/v1/tasks/"+id+"/diff", "owner")
			var diff Diff
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &diff) != nil {
				t.Fatalf("%d %.80s", out.Code, out.Body)
			}
			if !diff.Truncated || len(diff.Text) > MaxReadBytes || !strings.HasSuffix(diff.Text, tc.want) || strings.Contains(diff.Text, "secret-value") || strings.Contains(diff.Text, "QUJD") || strings.Contains(diff.Text, "-----BEGIN") {
				t.Fatalf("unsafe diff: length %d truncated %t suffix %.80q", len(diff.Text), diff.Truncated, diff.Text[max(0, len(diff.Text)-80):])
			}
		})
	}
	if err := os.WriteFile(filepath.Join(worktree, "file.txt"), []byte(`password="`+strings.Repeat("x", maxLogBytes)+`"`), 0600); err != nil {
		t.Fatal(err)
	}
	out := ownershipRequest(r, "GET", "/v1/tasks/"+id+"/diff", "owner")
	if out.Code != 503 || !strings.Contains(out.Body.String(), "20 MiB") {
		t.Fatalf("oversized diff: %d %.80s", out.Code, out.Body)
	}
}
