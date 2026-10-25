package devrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

func TestLoadDevRunner(t *testing.T) {
	base := "socket: /run/dev.sock\nsocket_group: opsgate\nwork_dir: /var/lib/opsgate-dev\nprojects:\n  - name: app\n    path: /srv/app\nagents:\n  codex:\n    command: [/usr/local/bin/codex, exec, '-']\n"
	for _, tc := range []struct {
		name, from, to, suffix string
		valid                  bool
	}{
		{name: "defaults", valid: true},
		{name: "admins", suffix: "admins: ['github:123', 'static:operator']\n", valid: true},
		{name: "empty admin", suffix: "admins: ['']\n"},
		{name: "duplicate admin", suffix: "admins: [owner, owner]\n"},
		{name: "admin whitespace", suffix: "admins: [' owner']\n"},
		{name: "admin control", suffix: `admins: ["owner\nadmin"]`},
		{name: "long admin", suffix: "admins: ['" + strings.Repeat("a", 257) + "']\n"},
		{name: "unknown", suffix: "unknown: true\n"},
		{name: "multiple documents", suffix: "---\n{}\n"},
		{name: "relative socket", from: "/run/dev.sock", to: "dev.sock"},
		{name: "relative workdir", from: "/var/lib/opsgate-dev", to: "dev"},
		{name: "relative project", from: "/srv/app", to: "app"},
		{name: "bad project name", from: "name: app", to: "name: App"},
		{name: "long project name", from: "name: app", to: "name: " + strings.Repeat("a", 64)},
		{name: "duplicate project", from: "agents:", to: "  - name: app\n    path: /srv/other\nagents:"},
		{name: "bad agent name", from: "  codex:", to: "  CODEX:"},
		{name: "relative executable", from: "/usr/local/bin/codex", to: "codex"},
		{name: "empty command", from: "[/usr/local/bin/codex, exec, '-']", to: "[]"},
		{name: "timeout", suffix: "timeout: 0s\n"},
		{name: "negative timeout", suffix: "timeout: -1s\n"},
		{name: "concurrency zero", suffix: "max_concurrent: 0\n"},
		{name: "concurrency high", suffix: "max_concurrent: 9\n"},
		{name: "retention zero", suffix: "max_tasks: 0\n"},
		{name: "missing group", from: "socket_group: opsgate\n", to: ""},
		{name: "base option", from: "path: /srv/app", to: "path: /srv/app\n    base_ref: --help"},
		{name: "invalid env", from: "command:", to: "env: [INVALID]\n    command:"},
		{name: "valid maximum", suffix: "max_concurrent: 8\ntimeout: 2h\nmax_tasks: 3\n", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			if tc.from != "" {
				s = strings.Replace(s, tc.from, tc.to, 1)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(s+tc.suffix), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := config.LoadDevRunner(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if tc.name == "defaults" && (c.Timeout != time.Hour || c.MaxConcurrent != 1 || c.MaxTasks != 100 || c.Projects[0].BaseRef != "HEAD") {
				t.Fatalf("defaults: %+v", c)
			}
		})
	}
}
