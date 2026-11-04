package devrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/redact"
)

func TestTaskMessageRedactsFragments(t *testing.T) {
	for _, tc := range []struct {
		name, file, source, want string
		oversized                bool
	}{
		{"quoted log tail", "log.txt", `password="` + strings.Repeat("x", 9000) + `secret-suffix"` + "\nfinished", redact.Mask + "\"\nfinished", false},
		{"quoted output prefix", "output.txt", `password="` + strings.Repeat("x", MaxReadBytes) + `secret-suffix"` + "\nfinished", `password="` + redact.Mask, false},
		{"PEM log tail", "log.txt", "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("x", 9000) + "secret-suffix", redact.Mask, false},
		{"PEM output prefix", "output.txt", "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat("x", MaxReadBytes) + "secret-suffix\n-----END PRIVATE KEY-----\nfinished", redact.Mask + "\n" + redact.Mask, false},
		{"short output", "output.txt", "ordinary final output\n", "ordinary final output\n", false},
		{"short log", "log.txt", "ordinary log output\n", "ordinary log output\n", false},
		{"short PEM output", "output.txt", "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----", redact.Mask, false},
		{"long output", "output.txt", strings.Repeat("x", MaxReadBytes) + "finished", strings.Repeat("x", MaxReadBytes), false},
		{"long log", "log.txt", strings.Repeat("x", MaxReadBytes) + "\nfinished", strings.Repeat("x", (8<<10)-len("\nfinished")) + "\nfinished", false},
		{"oversized output", "output.txt", "unsafe fragment", "task message omitted: source exceeds 20 MiB safe read limit", true},
		{"oversized log", "log.txt", "unsafe fragment", "task message omitted: source exceeds 20 MiB safe read limit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `cat >/dev/null; cp "$SOURCE" "$1"; echo fallback`
			if tc.file == "log.txt" {
				script = `cat >/dev/null; cp "$SOURCE" "$(dirname "$1")/log.txt"`
			}
			cfg, _ := runnerConfig(t, script)
			source := filepath.Join(filepath.Dir(cfg.Socket), "source")
			if err := os.WriteFile(source, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.oversized {
				if err := os.Truncate(source, maxLogBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			a := cfg.Agents["fake"]
			a.Env = []string{"SOURCE=" + source}
			cfg.Agents["fake"] = a
			if err := os.MkdirAll(filepath.Join(cfg.WorkDir, "tasks"), 0700); err != nil {
				t.Fatal(err)
			}
			h := launchCore(t, cfg)
			task := h.start(t, "finish task")
			task = h.wait(t, task.ID, "succeeded")
			if task.Message != tc.want {
				t.Fatalf("message length %d, want %d; secret visible: %t; prefix: %.80q", len(task.Message), len(tc.want), strings.Contains(task.Message, "secret-suffix"), task.Message)
			}
		})
	}
}

func TestCapturedLogTruncatedQuotedValue(t *testing.T) {
	cfg, _ := runnerConfig(t, `cat >/dev/null; cat "$SOURCE"`)
	source := filepath.Join(filepath.Dir(cfg.Socket), "source")
	prefix := strings.Repeat("x\n", (maxLogBytes-(16<<10))/2)
	text := prefix + `password="` + strings.Repeat("s", 16<<10) + `"`
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a := cfg.Agents["fake"]
	a.Env = []string{"SOURCE=" + source}
	cfg.Agents["fake"] = a
	if err := os.MkdirAll(filepath.Join(cfg.WorkDir, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	h := launchCore(t, cfg)
	task := h.start(t, "finish task")
	task = h.wait(t, task.ID, "succeeded")
	info, err := os.Stat(filepath.Join(cfg.WorkDir, "tasks", task.ID, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxLogBytes {
		t.Fatalf("log size = %d, want %d", info.Size(), maxLogBytes)
	}
	if task.Message != redact.Mask {
		t.Errorf("final message = %.80q, want %q", task.Message, redact.Mask)
	}
	for _, tc := range []struct {
		offset int
		want   string
	}{
		{len(prefix), `password="` + redact.Mask},
		{maxLogBytes - 32, redact.Mask},
	} {
		var page Log
		path := fmt.Sprintf("/v1/tasks/%s/log?offset=%d&limit=64", task.ID, tc.offset)
		if code := h.request(t, "GET", path, nil, &page); code != 200 {
			t.Fatalf("log status = %d", code)
		}
		end := min(tc.offset+64, maxLogBytes)
		if page.Text != tc.want || page.NextOffset != int64(end) || page.Truncated != (end < maxLogBytes) {
			t.Errorf("offset %d: page = %+v, want text %q, next %d", tc.offset, page, tc.want, end)
		}
	}
}
