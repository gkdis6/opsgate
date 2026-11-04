package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/redact"
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
		{name: "filter", in: logsInput{Since: "1d", Priority: "err", Grep: "FAIL [x]"}, tail: []string{"-n", "200", "--since", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).In(time.Local).Format("2006-01-02 15:04:05"), "-p", "err"}},
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

func TestLogsGrepWindow(t *testing.T) {
	for _, tt := range []struct {
		name, journal, grep, body        string
		maximum, fetch, scanned, matched int
	}{
		{"configured", "hit old\nHIT middle\nhit newest\nother\nother\n", "hit", "HIT middle\nhit newest\n", 5, 5, 5, 3},
		{"default", "hit old\nother\nother\n", "hit", "hit old\n", 0, 500, 3, 1},
		{"no matches", "other\nother\n", "hit", "", 5, 5, 2, 0},
		{"empty", "", "hit", "", 5, 5, 0, 0},
		{"unfiltered", "one\ntwo\n", "", "one\ntwo\n", 5, 2, 2, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := provider{cfg: config.Host{Units: []string{"nginx"}, MaxLogLines: tt.maximum}, run: func(_ context.Context, name string, args ...string) (string, error) {
				want := []string{"-u", "nginx.service", "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(tt.fetch)}
				if name != "journalctl" || !reflect.DeepEqual(args, want) {
					t.Fatalf("command %s %q, want %q", name, args, want)
				}
				return tt.journal, nil
			}}
			out, err := p.logs(context.Background(), logsInput{Unit: "nginx", Lines: 2, Grep: tt.grep})
			header, body, _ := strings.Cut(out, "\n")
			counts := fmt.Sprintf("scanned: %d; matched: %d;", tt.scanned, tt.matched)
			if err != nil || body != tt.body || !strings.Contains(header, counts) {
				t.Fatalf("got %q, %v", out, err)
			}
		})
	}
}

func TestLogsRedactBeforeFiltering(t *testing.T) {
	pem := "-----BEGIN PRIVATE KEY-----\nMIIEvQ/abcKEYBODY\n-----END PRIVATE KEY-----\n"
	for _, tt := range []struct {
		name, source, grep, body string
		lines, scanned, matched  int
	}{
		{name: "prefix h", source: "password=hunter2\n", grep: "password=h", scanned: 1},
		{name: "prefix hu", source: "password=hunter2\n", grep: "password=hu", scanned: 1},
		{name: "wrong prefix", source: "password=hunter2\n", grep: "password=x", scanned: 1},
		{name: "PEM body", source: pem, grep: "/", scanned: 3},
		{name: "PEM without begin", source: "MIIEvQ/abcKEYBODY\n-----END PRIVATE KEY-----\n", grep: "/", scanned: 2},
		{name: "PEM without end", source: "-----BEGIN PRIVATE KEY-----\nMIIEvQ/abcKEYBODY", grep: "/", scanned: 2},
		{name: "ordinary text", source: "ok\nERROR password=hunter2\n", grep: "error", body: "ERROR password=" + redact.Mask + "\n", scanned: 2, matched: 1},
		{name: "masked text", source: "password=hunter2\n", grep: "redacted", body: "password=" + redact.Mask + "\n", scanned: 1, matched: 1},
		{name: "PEM lines", source: "before\n" + pem + "after\n", body: "before\n" + strings.Repeat(redact.Mask+"\n", 3) + "after\n", scanned: 5, matched: 5},
		{name: "tail inside PEM", source: "before\n" + pem, lines: 2, body: strings.Repeat(redact.Mask+"\n", 2), scanned: 2, matched: 2},
		{name: "tail matching lines", source: "ERROR old\npassword=hunter2\nerror new\n", lines: 1, grep: "error", body: "error new\n", scanned: 3, matched: 2},
		{name: "empty", grep: "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := provider{cfg: config.Host{Units: []string{"nginx"}, MaxLogLines: 5}, run: func(context.Context, string, ...string) (string, error) {
				return tt.source, nil
			}}
			lines := tt.lines
			if lines == 0 {
				lines = 5
			}
			out, err := p.logs(context.Background(), logsInput{Unit: "nginx", Lines: lines, Grep: tt.grep})
			want := fmt.Sprintf("unit: nginx.service; lines: %d; scanned: %d; matched: %d; since: %q; priority: %q; grep: %q\n%s", lines, tt.scanned, tt.matched, "", "", tt.grep, tt.body)
			if err != nil || out != want {
				t.Fatalf("got %q, %v; want %q", out, err, want)
			}
		})
	}
}

func TestServicesNotFound(t *testing.T) {
	for _, patterns := range [][]string{{"ssh*"}, {"ssh*", "sshd"}, {"sshd.service", "ssh*"}} {
		t.Run(strings.Join(patterns, ","), func(t *testing.T) {
			p := provider{cfg: config.Host{Units: patterns}, run: func(_ context.Context, name string, args ...string) (string, error) {
				if args[0] == "list-units" {
					return "ssh.service loaded active running SSH\nsshd.service not-found inactive dead Missing\nsshd-keygen.service not-found inactive dead Missing\n", nil
				}
				want := []string{"ssh.service"}
				out := "Id=ssh.service\nLoadState=loaded\nActiveState=active\n"
				if len(patterns) > 1 {
					want = append(want, "sshd.service")
					out += "\nId=sshd.service\nLoadState=not-found\nActiveState=inactive\n"
				}
				if name != "systemctl" || !reflect.DeepEqual(args[4:], want) {
					t.Fatalf("unexpected command: %s %q", name, args)
				}
				return out, nil
			}}
			out, err := p.services(context.Background())
			if err != nil || !strings.Contains(out, "Id: ssh.service") || strings.Contains(out, "sshd-keygen.service") || strings.Contains(out, "Id: sshd.service") != (len(patterns) > 1) {
				t.Fatalf("got %q, %v", out, err)
			}
		})
	}
}
