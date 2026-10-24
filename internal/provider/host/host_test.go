package host

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOverview(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "no systemd"}[unavailable], func(t *testing.T) {
			p := provider{proc: "testdata/proc", etc: "testdata/etc", cfg: config.Host{Units: []string{"good", "bad"}, DiskPaths: []string{t.TempDir(), "/nonexistent/opsgate-test"}}, run: func(context.Context, string, ...string) (string, error) {
				if unavailable {
					return "", errors.New("systemctl unavailable")
				}
				return "Id=good.service\nActiveState=active\n\nId=bad.service\nActiveState=failed\n", nil
			}}
			out, err := p.overview(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"hostname: fixture-host", "OS: Fixture Linux 1.0", "kernel: 6.8.0-fixture", "uptime: 25h1m1s", "load averages: 0.10 0.20 0.30", "CPUs: 2", "total 1.0 GiB; available 256.0 MiB; used 75.0%", "swap: total 512.0 MiB; free 256.0 MiB; used 256.0 MiB (50.0%)", "inode use"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in %s", want, out)
				}
			}
			want := "1 active, 1 failed; failed units: bad.service"
			if unavailable {
				want = "units: unavailable: systemctl unavailable"
			}
			if !strings.Contains(out, want) {
				t.Fatal(out)
			}
		})
	}
}
func TestOverviewNoFailedUnits(t *testing.T) {
	for _, active := range []bool{false, true} {
		p := provider{proc: "testdata/proc", etc: "testdata/etc"}
		want := "units: 0 active, 0 failed\n"
		if active {
			p.cfg.Units = []string{"good"}
			p.run = func(context.Context, string, ...string) (string, error) {
				return "Id=good.service\nActiveState=active\n", nil
			}
			want = "units: 1 active, 0 failed\n"
		}
		out, err := p.overview(context.Background())
		if err != nil || !strings.HasSuffix(out, want) || strings.Contains(out, "failed units:") {
			t.Fatalf("got %q, %v", out, err)
		}
	}
}

func TestRegister(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	if err := Register(s, &tool.Env{}, config.Host{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{"host_overview": true, "host_services": true, "host_service_status": true, "host_logs": true, "host_top_processes": true}
	if len(result.Tools) != len(names) {
		t.Fatal(result.Tools)
	}
	wantFields := map[string]map[string][]string{
		"host_service_status": {"unit": {"allowlisted", "default", "256"}},
		"host_logs": {
			"unit":     {"allowlisted", "default", "256"},
			"lines":    {"100", "500", "maximum"},
			"since":    {"duration", "RFC3339", "128", "Default"},
			"priority": {"emerg", "debug", "0-7", "Default"},
			"grep":     {"substring", "4096", "default"},
		},
		"host_top_processes": {"sort": {"cpu", "mem", "default"}, "limit": {"integer", "15", "50"}},
	}
	for _, tool := range result.Tools {
		if !names[tool.Name] || tool.Title == "" || tool.Description == "" || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || *tool.Annotations.DestructiveHint {
			t.Fatalf("invalid metadata: %+v", tool)
		}
		if !strings.HasPrefix(tool.Description, "Return ") {
			t.Fatalf("tool %s does not describe its result: %s", tool.Name, tool.Description)
		}
		data, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct{ Description string }
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema.Properties) != len(wantFields[tool.Name]) {
			t.Fatalf("unexpected fields for %s: %s", tool.Name, data)
		}
		for field, terms := range wantFields[tool.Name] {
			for _, term := range terms {
				if !strings.Contains(schema.Properties[field].Description, term) {
					t.Errorf("%s.%s description missing %q: %s", tool.Name, field, term, data)
				}
			}
		}
	}
	s2 := mcp.NewServer(&mcp.Implementation{Name: "bad", Version: "1"}, nil)
	if err := Register(s2, &tool.Env{}, config.Host{Units: []string{"["}}); err == nil {
		t.Fatal("malformed allowlist accepted")
	}
}
func TestCommandBounds(t *testing.T) {
	w := cappedWriter{remaining: 4}
	if _, err := w.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("cdef")); err == nil || w.String() != "abcd" {
		t.Fatalf("%q %v", w.String(), err)
	}
	if _, err := runCommand(context.Background(), "opsgate-nonexistent-system-binary"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal(err)
	}
}
