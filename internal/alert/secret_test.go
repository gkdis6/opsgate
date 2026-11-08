package alert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoticeSecretBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if _, err := ReadSecret(path); err == nil {
		t.Fatal("missing secret accepted")
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"", false}, {"  short\n", false}, {"0123456789abcdef\n", true},
		{strings.Repeat("k", 4096) + "\n", true}, {strings.Repeat("k", 4097), false}, {strings.Repeat(" ", 8193), false},
	} {
		if err := os.WriteFile(path, []byte(tc.value), 0600); err != nil {
			t.Fatal(err)
		}
		key, err := ReadSecret(path)
		if (err == nil) != tc.valid {
			t.Fatal("secret bounds", len(tc.value), err)
		}
		if tc.valid && string(key) != strings.TrimSpace(tc.value) {
			t.Fatal("key not trimmed")
		}
	}
}
