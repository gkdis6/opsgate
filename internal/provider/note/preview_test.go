package note

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
)

func TestPreviewHashesMaskSecrets(t *testing.T) {
	var previous []string
	for _, secret := range []string{"weak-guess-one", "weak-guess-two"} {
		before := "password=" + secret + "\nvisible\n"
		after := before + "added\n"
		d := writePlan{action: "append", name: "note.md", resolved: "note.md", hash: sha256.Sum256([]byte(before)), text: after}
		plan := preview(d, before, after)
		var hashes []string
		for _, line := range strings.Split(plan.Summary, "\n") {
			if strings.Contains(line, "sha256:") {
				hash, _, _ := strings.Cut(line, ";")
				hashes = append(hashes, hash)
			}
		}
		currentHash := sha256.Sum256([]byte(redact.String(before)))
		resultHash := sha256.Sum256([]byte(redact.String(after)))
		want := []string{fmt.Sprintf("current sha256: %x", currentHash[:8]), fmt.Sprintf("result sha256: %x", resultHash)}
		if len(hashes) != 2 || strings.Join(hashes, "\n") != strings.Join(want, "\n") {
			t.Errorf("preview hashes = %v, want masked hashes %v", hashes, want)
		}
		if previous != nil && strings.Join(hashes, "\n") != strings.Join(previous, "\n") {
			t.Error("preview hashes distinguish masked secret values")
		}
		if plan.Data.(writePlan).hash != sha256.Sum256([]byte(before)) {
			t.Error("raw snapshot hash changed")
		}
		previous = hashes
	}
}

func TestCreatePreviewDiff(t *testing.T) {
	for _, tc := range []struct {
		name, content, diff string
	}{
		{"empty", "", "--- /dev/null\n+++ note.md\n"},
		{"newline", "- item\n---\n-A INPUT\n", "--- /dev/null\n+++ note.md\n@@ -0,0 +1,3 @@\n+- item\n+---\n+-A INPUT\n"},
		{"no newline", "new", "--- /dev/null\n+++ note.md\n@@ -0,0 +1,1 @@\n+new\n\\ No newline at end of file\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := writePlan{action: "create", name: "note.md", resolved: "note.md"}
			plan := preview(d, "", tc.content)
			hash := sha256.Sum256([]byte(tc.content))
			want := fmt.Sprintf("create note note.md\ncurrent sha256: absent\nresult sha256: %x\n", hash) + tc.diff
			if plan.Summary != want || plan.FullReviewRequired {
				t.Fatalf("preview = %q, full review = %v; want %q", plan.Summary, plan.FullReviewRequired, want)
			}
		})
	}
}
