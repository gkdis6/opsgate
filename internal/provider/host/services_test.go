package host

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

func TestUnits(t *testing.T) {
	for _, tt := range []struct {
		name           string
		patterns, want []string
		list           bool
	}{
		{"empty", nil, []string{}, false}, {"literals", []string{"offline", "nginx.service", "offline"}, []string{"nginx.service", "offline.service"}, false},
		{"glob", []string{"web*", "offline"}, []string{"offline.service", "web-a.service", "web-b.timer"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			p := provider{cfg: config.Host{Units: tt.patterns}, run: func(_ context.Context, name string, args ...string) (string, error) {
				called = true
				if name != "systemctl" || !reflect.DeepEqual(args, []string{"list-units", "--all", "--plain", "--no-legend", "--no-pager", "--full"}) {
					t.Fatalf("unexpected command: %s %q", name, args)
				}
				return "web-a.service loaded active running Web\nweb-b.timer loaded inactive dead Timer\nsecret.service loaded active running Secret\n--bad.service loaded active running Bad\n", nil
			}}
			got, err := p.units(context.Background())
			if err != nil || !reflect.DeepEqual(got, tt.want) || called != tt.list {
				t.Fatalf("got %q, %v called=%v", got, err, called)
			}
		})
	}
}
func TestDeniedNoCommand(t *testing.T) {
	p := provider{cfg: config.Host{Units: []string{"allowed"}}, run: func(context.Context, string, ...string) (string, error) {
		t.Fatal("command called before authorization")
		return "", nil
	}}
	for _, unit := range []string{"secret", "nonexistent"} {
		if _, err := p.status(context.Background(), unit); !errors.Is(err, tool.ErrDenied) {
			t.Fatal(err)
		}
		if _, err := p.logs(context.Background(), logsInput{Unit: unit}); !errors.Is(err, tool.ErrDenied) {
			t.Fatal(err)
		}
	}
}
func TestSince(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		in       string
		ago      time.Duration
		absolute string
		bad      bool
	}{
		{in: "30m", ago: 30 * time.Minute}, {in: "2h", ago: 2 * time.Hour}, {in: "1d", ago: 24 * time.Hour}, {in: "1.5d", ago: 36 * time.Hour},
		{in: "1d2h30m", ago: 26*time.Hour + 30*time.Minute},
		{in: ".5d", ago: 12 * time.Hour},
		{in: "2026-10-23T10:00:00+02:00", absolute: "2026-10-23T08:00:00Z"},
		{in: "yesterday", bad: true}, {in: "now", bad: true}, {in: "-1h", bad: true}, {in: "0s", bad: true}, {in: "1d;id", bad: true}, {in: "999999999999d", bad: true}, {in: "2026-10-24", bad: true},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := absoluteSince(tt.in, now)
			if tt.bad {
				if err == nil {
					t.Fatalf("accepted %q", tt.in)
				}
				return
			}
			want := now.Add(-tt.ago)
			if tt.absolute != "" {
				want, _ = time.Parse(time.RFC3339, tt.absolute)
			}
			if err != nil || got != want.In(time.Local).Format("2006-01-02 15:04:05") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}
func TestLogs(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   logsInput
		tail []string
		bad  bool
	}{
		{name: "defaults", in: logsInput{}, tail: []string{"-n", "100"}},
		{name: "clamp high", in: logsInput{Lines: 9999}, tail: []string{"-n", "200"}},
		{name: "clamp low", in: logsInput{Lines: -1}, tail: []string{"-n", "1"}},
		{name: "filter", in: logsInput{Since: "1d", Priority: "err", Grep: "FAIL [x]"}, tail: []string{"-n", "100", "--since", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).In(time.Local).Format("2006-01-02 15:04:05"), "-p", "err"}},
		{name: "bad priority", in: logsInput{Priority: "--help"}, bad: true}, {name: "bad since", in: logsInput{Since: "today"}, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Unit = "nginx"
			called := false
			p := provider{cfg: config.Host{Units: []string{"nginx"}, MaxLogLines: 200}, now: func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }, run: func(_ context.Context, name string, args ...string) (string, error) {
				called = true
				want := append([]string{"-u", "nginx.service", "--no-pager", "-o", "short-iso"}, tt.tail...)
				if name != "journalctl" || !reflect.DeepEqual(args, want) {
					t.Fatalf("command %s %q, want %q", name, args, want)
				}
				return "ok\nFAIL [x] broken\nfail ax different\n", nil
			}}
			out, err := p.logs(context.Background(), tt.in)
			if tt.bad {
				if err == nil || called {
					t.Fatal("invalid input ran command")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.in.Grep != "" && (!strings.Contains(out, "FAIL [x] broken") || strings.Contains(out, "fail ax") || strings.Contains(out, "\nok\n")) {
				t.Fatal(out)
			}
		})
	}
}
func TestProperties(t *testing.T) {
	p := provider{cfg: config.Host{Units: []string{"nginx"}}, run: func(_ context.Context, name string, args ...string) (string, error) {
		if !reflect.DeepEqual(args, []string{"show", "--no-pager", "--property=" + detailProperties, "--", "nginx.service"}) {
			t.Fatal(args)
		}
		return "Id=nginx.service\nDescription=A=B\nMemoryCurrent=1048576\nActiveState=active\n\n", nil
	}}
	out, err := p.status(context.Background(), "nginx")
	if err != nil || !strings.Contains(out, "Description: A=B") || !strings.Contains(out, "MemoryCurrent: 1.0 MiB") {
		t.Fatalf("%s, %v", out, err)
	}
	rows := parseProperties("Id=a.service\n\nId=b.service\nMemoryCurrent=[not set]\n")
	if len(rows) != 2 || rows[1]["Id"] != "b.service" {
		t.Fatal(rows)
	}
}
