package host

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRestart(t *testing.T) {
	for _, prefix := range [][]string{nil, {"/usr/bin/sudo", "-n"}} {
		t.Run(strings.Join(prefix, " "), func(t *testing.T) {
			var calls [][]string
			p := provider{resolve: fakeSystemctl, cfg: config.Host{RestartUnits: []string{"nginx*"}, CommandPrefix: prefix}, run: func(_ context.Context, binary string, args ...string) (string, error) {
				calls = append(calls, append([]string{binary}, args...))
				return "Id=nginx.service\nLoadState=loaded\nActiveState=active\nSubState=running\nActiveEnterTimestamp=2026-10-24 01:00:00 UTC\n", nil
			}}
			in := restartInput{Unit: "nginx"}
			plan, err := p.planRestart(context.Background(), tool.Caller{}, in)
			hostname, _ := os.Hostname()
			want := "Restart systemd unit nginx.service on host " + hostname + " (currently active/running, since 2026-10-24 01:00:00 UTC)"
			if err != nil || plan.Summary != want {
				t.Fatalf("%+v %v", plan, err)
			}
			if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"systemctl", "show", "--no-pager", "--property=" + basicProperties, "--", "nginx.service"}) {
				t.Fatalf("Plan commands: %v", calls)
			}
			out, err := p.applyRestart(context.Background(), tool.Caller{}, in, plan)
			if err != nil || !strings.Contains(out, "active/running") {
				t.Fatalf("%s %v", out, err)
			}
			binary := "/usr/bin/systemctl"
			wantArgv := append(append([]string{}, prefix...), binary, "restart", "--", "nginx.service")
			if len(calls) != 3 || !reflect.DeepEqual(calls[1], wantArgv) {
				t.Fatalf("%v want %v", calls, wantArgv)
			}
		})
	}
}

func TestRestartDenials(t *testing.T) {
	for _, tc := range []struct {
		name, unit string
		cfg        config.Host
		denied     bool
	}{
		{"empty allowlist", "nginx", config.Host{Units: []string{"*"}}, true},
		{"read independent", "other", config.Host{Units: []string{"*"}, RestartUnits: []string{"nginx"}}, true},
		{"option", "--help", config.Host{RestartUnits: []string{"*"}}, false},
		{"newline", "nginx\n", config.Host{RestartUnits: []string{"*"}}, false},
		{"relative prefix", "nginx", config.Host{RestartUnits: []string{"*"}, CommandPrefix: []string{"sudo", "-n"}}, false},
		{"flag executable", "nginx", config.Host{RestartUnits: []string{"*"}, CommandPrefix: []string{"-n"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := provider{cfg: tc.cfg, run: func(context.Context, string, ...string) (string, error) {
				t.Fatal("denied input executed command")
				return "", nil
			}}
			_, err := p.planRestart(context.Background(), tool.Caller{}, restartInput{Unit: tc.unit})
			if err == nil || tc.denied && !errors.Is(err, tool.ErrDenied) {
				t.Fatal(err)
			}
			_, err = p.applyRestart(context.Background(), tool.Caller{}, restartInput{Unit: tc.unit}, tool.Plan{})
			if err == nil {
				t.Fatal("Apply accepted denied input")
			}
		})
	}
	for _, cfg := range []config.Host{{RestartUnits: []string{"["}}, {CommandPrefix: []string{"-n"}}} {
		if err := Register(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil), &tool.Env{}, cfg); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}

func TestRestartFailures(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		calls := 0
		p := provider{resolve: fakeSystemctl, cfg: config.Host{RestartUnits: []string{"*"}}, run: func(context.Context, string, ...string) (string, error) {
			calls++
			if calls == failAt {
				return "", errors.New("command failed")
			}
			return "ActiveState=active\nSubState=running\n", nil
		}}
		if _, err := p.planRestart(context.Background(), tool.Caller{}, restartInput{Unit: "nginx"}); err != nil {
			if failAt != 1 {
				t.Fatal(err)
			}
			continue
		}
		if _, err := p.applyRestart(context.Background(), tool.Caller{}, restartInput{Unit: "nginx"}, tool.Plan{}); err == nil {
			t.Fatal("failure ignored")
		}
	}
}

func fakeSystemctl(string) (string, error) { return "/usr/bin/systemctl", nil }
