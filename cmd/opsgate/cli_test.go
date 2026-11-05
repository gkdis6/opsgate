package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/buildinfo"
)

func testApp() (*application, *bytes.Buffer, *bytes.Buffer, *int) {
	out, errout := new(bytes.Buffer), new(bytes.Buffer)
	calls := new(int)
	c := &application{stdin: strings.NewReader(""), stdout: out, stderr: errout,
		server: func(string, string) error {
			(*calls)++
			return nil
		},
	}
	return c, out, errout, calls
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{
		{"-h"}, {"-help"}, {"watch", "-h"}, {"dev-runner", "-h"}, {"init", "-h"},
		{"config", "-h"}, {"config", "show", "-h"}, {"passkey", "-h"}, {"passkey", "invite", "-h"},
		{"version", "-h"}, {"help", "-h"}, {"help"}, {"help", "watch"},
		{"help", "config"}, {"help", "config", "show"}, {"help", "passkey", "invite"},
		{"help", "version"}, {"help", "passkey"}, {"help", "help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c, out, errout, calls := testApp()
			if code := c.execute(args); code != 0 {
				t.Fatalf("exit %d: %s", code, errout)
			}
			if !strings.Contains(out.String(), "Usage:") || errout.Len() != 0 || *calls != 0 {
				t.Fatalf("stdout=%q stderr=%q server calls=%d", out, errout, *calls)
			}
			if len(args) == 1 && args[0] == "-h" {
				for _, name := range []string{"dev-runner", "watch", "init", "config", "passkey", "version", "help", "-config", "-transport", "-version", "-gen-token"} {
					if !strings.Contains(out.String(), name) {
						t.Errorf("root help missing %q", name)
					}
				}
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"wat"}, {"config"}, {"passkey"}, {"config", "typo"}, {"passkey", "typo"},
		{"config", "typo", "-config", "x.yaml"}, {"passkey", "typo", "-config", "x.yaml"},
		{"-bogus"}, {"-config"}, {"-config", "x.yaml", "-transport", "bogus"}, {"-config", "x.yaml", "extra"}, {"-version", "extra"}, {"-gen-token", "extra"}, {"--", "extra"},
		{"dev-runner"}, {"watch"}, {"passkey", "invite"}, {"passkey", "invite", "-config", "x.yaml"},
		{"passkey", "invite", "-subject", "passkey:alice"}, {"init", "-yes"},
		{"dev-runner", "-config", "x.yaml", "extra"}, {"watch", "-config", "x.yaml", "extra"},
		{"init", "extra"}, {"config", "show", "-config", "x.yaml", "extra"},
		{"-config", "x.yaml", "watch"}, {"init", "-force=invalid"},
		{"passkey", "invite", "-config", "x.yaml", "-subject", "passkey:alice", "extra"},
		{"version", "extra"}, {"help", "version", "extra"}, {"help", "wat"}, {"help", "passkey", "typo"},
		{"help", "config", "show", "extra"},
		{"config", "-bogus"}, {"passkey", "-bogus"}, {"watch", "-bogus"}, {"dev-runner", "-bogus"},
		{"init", "-bogus"}, {"config", "show", "-bogus"}, {"passkey", "invite", "-ttl", "invalid"}, {"version", "-bogus"},
		{"passkey", "invite", "-config", "x.yaml", "-subject", "passkey:alice", "-ttl", "0"},
		{"passkey", "invite", "-config", "x.yaml", "-subject", "passkey:alice", "-ttl", "-1h"},
		{"passkey", "invite", "-config", "x.yaml", "-subject", "passkey:alice", "-ttl", "169h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c, out, errout, calls := testApp()
			if code := c.execute(args); code != 2 {
				t.Fatalf("exit %d: stdout=%q stderr=%q", code, out, errout)
			}
			if out.Len() != 0 || *calls != 0 {
				t.Fatalf("stdout=%q server calls=%d", out, *calls)
			}
			lines := strings.Split(strings.TrimSpace(errout.String()), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "opsgate: ") || !strings.HasPrefix(lines[1], "run 'opsgate ") || !strings.Contains(lines[1], "-h'") {
				t.Fatalf("unexpected diagnostic: %q", errout)
			}
		})
	}
}

func TestRouting(t *testing.T) {
	for _, path := range []string{"dev-runner", "watch", "init", "config show", "passkey invite", "version", "help"} {
		t.Run(path, func(t *testing.T) {
			c, _, errout, _ := testApp()
			commands := c.commands()
			got := ""
			var gotArgs []string
			var stub func([]command, string)
			stub = func(commands []command, prefix string) {
				for i := range commands {
					cmd := &commands[i]
					name := strings.TrimSpace(prefix + " " + cmd.name)
					if len(cmd.children) != 0 {
						stub(cmd.children, name)
						continue
					}
					cmd.run = func(args []string) error { got = name; gotArgs = args; return nil }
				}
			}
			stub(commands, "")
			args := append(strings.Fields(path), "-config", "example.yaml")
			if code := c.dispatch(args, commands); code != 0 {
				t.Fatalf("exit %d: %s", code, errout)
			}
			if got != path || !reflect.DeepEqual(gotArgs, []string{"-config", "example.yaml"}) {
				t.Fatalf("route=%q args=%v", got, gotArgs)
			}
		})
	}
}

func TestRootServer(t *testing.T) {
	for _, args := range [][]string{nil, {"-config", "f.yaml"}, {"-config", "f.yaml", "-transport", "http"}} {
		c, out, errout, _ := testApp()
		c.server = func(path, transport string) error {
			if len(args) != 0 && path != "f.yaml" {
				t.Errorf("config=%q", path)
			}
			if len(args) == 4 && transport != "http" {
				t.Errorf("transport=%q", transport)
			}
			return errors.New("server failed")
		}
		if code := c.execute(args); code != 1 || out.Len() != 0 || errout.String() != "opsgate: server failed\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errout)
		}
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-version"}} {
		var out, errout bytes.Buffer
		if code := cli(args, strings.NewReader(""), &out, &errout); code != 0 || out.String() != buildinfo.Version()+"\n" || errout.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, &out, &errout)
		}
	}
}

func TestInitStreams(t *testing.T) {
	c, out, errout, _ := testApp()
	args := []string{"init", "-yes", "-public-url", "https://example.com", "-login", "passkey", "-user", "alice", "-o", "-"}
	if code := c.execute(args); code != 0 || !strings.Contains(out.String(), "mode: kubernetes") || errout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errout)
	}
}

func TestInitNamespaceDenies(t *testing.T) {
	for _, override := range []bool{false, true} {
		c, out, errout, _ := testApp()
		args := []string{"init", "-yes", "-public-url", "https://example.com", "-login", "passkey", "-user", "alice", "-o", "-"}
		want := `deny_namespaces: ["kube-system", "kube-public", "kube-node-lease", "opsgate"]`
		if override {
			args = append(args, "-deny-namespaces", "private-*")
			want = `deny_namespaces: ["private-*"]`
		}
		if code := c.execute(args); code != 0 || errout.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", code, errout)
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("generated config missing %q: %s", want, out)
		}
	}
}

func TestPasskeyInviteOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opsgate.yaml")
	data := fmt.Sprintf(`mode: kubernetes
http:
  public_url: https://ops.example
auth:
  builtin:
    state_file: %q
    users:
      - subject: passkey:alice
        scopes: [ops:read]
`, filepath.Join(dir, "state.json"))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, out, errout, _ := testApp()
	if code := c.execute([]string{"passkey", "invite", "-config", path, "-subject", "passkey:alice"}); code != 0 || errout.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, errout)
	}
	u, err := url.Parse(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("invite")
	if b, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(b) != 32 {
		t.Fatalf("invite token = %q, err = %v", token, err)
	}
	if out.String() != "https://ops.example/passkey/register?invite="+token+"\n" {
		t.Fatalf("invite output = %q", out)
	}
	key := sha256.Sum256([]byte(token))
	if _, err := os.Stat(filepath.Join(dir, "passkey-invites", fmt.Sprintf("%x.json", key))); err != nil {
		t.Fatalf("invite not stored for printed token: %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputFailure(t *testing.T) {
	if code := cli([]string{"version"}, strings.NewReader(""), failingWriter{}, io.Discard); code != 1 {
		t.Fatalf("exit=%d", code)
	}
}

func TestOperationalErrors(t *testing.T) {
	missing := t.TempDir() + "/missing.yaml"
	for _, args := range [][]string{
		{"dev-runner", "-config", missing}, {"watch", "-config", missing},
		{"config", "show", "-config", missing},
		{"passkey", "invite", "-config", missing, "-subject", "passkey:alice"},
	} {
		t.Run(strings.Join(args[:1], " "), func(t *testing.T) {
			c, out, errout, calls := testApp()
			if code := c.execute(args); code != 1 || out.Len() != 0 || *calls != 0 || !strings.Contains(errout.String(), missing) || strings.Contains(errout.String(), "for help") {
				t.Fatalf("exit=%d stdout=%q stderr=%q server calls=%d", code, out, errout, *calls)
			}
		})
	}
}
