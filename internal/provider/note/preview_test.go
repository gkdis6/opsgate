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
