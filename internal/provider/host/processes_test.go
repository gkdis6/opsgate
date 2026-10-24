package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func statLine(pid int, comm string, ticks, start, rss uint64) string {
	f := strings.Fields("S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 0")
	f[11] = strconv.FormatUint(ticks, 10)
	f[19] = strconv.FormatUint(start, 10)
	f[21] = strconv.FormatUint(rss, 10)
	return fmt.Sprintf("%d (%s) %s", pid, comm, strings.Join(f, " "))
}
func TestParseStat(t *testing.T) {
	for _, tt := range []struct {
		name, line string
		bad        bool
	}{
		{"normal", statLine(42, "worker", 50, 100, 3), false}, {"parentheses", statLine(42, "evil ) ( name", 50, 100, 3), false},
		{"short", "42 (worker) S 1", true}, {"no comm", "42 worker S", true}, {"bad pid", statLine(-1, "worker", 0, 0, 0), true},
		{"bad number", strings.Replace(statLine(42, "worker", 50, 100, 3), "50", "oops", 1), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStat(tt.line)
			if tt.bad {
				if err == nil {
					t.Fatal("accepted invalid stat")
				}
				return
			}
			if err != nil || got.pid != 42 || got.ticks != 50 || got.start != 100 || got.rss != 3*uint64(os.Getpagesize()) || got.state != "S" {
				t.Fatalf("%+v, %v", got, err)
			}
		})
	}
}
func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}
func processFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "stat"), "cpu 100 0 100 800 0 0 0 0 50 0\ncpu0 50 0 50 400\ncpu1 50 0 50 400\n")
	for _, pid := range []int{10, 20} {
		dir := filepath.Join(root, strconv.Itoa(pid))
		writeFile(t, filepath.Join(dir, "stat"), statLine(pid, "stat name", 50, 100, uint64(pid)))
		writeFile(t, filepath.Join(dir, "comm"), fmt.Sprintf("worker-%d\n", pid))
		writeFile(t, filepath.Join(dir, "status"), "Name: worker\nUid:\t4294967294\t4294967294\t4294967294\t4294967294\n")
		writeFile(t, filepath.Join(dir, "cmdline"), "TOP-SECRET-ARGUMENT")
		writeFile(t, filepath.Join(dir, "environ"), "TOP-SECRET-ENV")
	}
	writeFile(t, filepath.Join(root, "30", "stat"), "raced-away")
	return root
}
func TestTop(t *testing.T) {
	root := processFixture(t)
	for _, tt := range []struct {
		name  string
		in    topInput
		first string
		count int
		bad   bool
	}{
		{"default", topInput{}, "10", 2, false}, {"memory", topInput{Sort: "mem", Limit: 1}, "20", 1, false},
		{"cpu tie", topInput{Sort: "cpu", Limit: 999}, "10", 2, false}, {"low limit", topInput{Limit: -3}, "10", 1, false}, {"invalid sort", topInput{Sort: "rss"}, "", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := provider{proc: root, pause: func(context.Context) error { return nil }}
			out, err := p.top(context.Background(), tt.in)
			if tt.bad {
				if err == nil {
					t.Fatal("accepted sort")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) != tt.count+1 || strings.Fields(lines[1])[0] != tt.first || strings.Contains(out, "TOP-SECRET") || strings.Contains(out, "stat name") || !strings.Contains(out, "worker-") {
				t.Fatal(out)
			}
		})
	}
	p := provider{proc: root, pause: func(ctx context.Context) error { return waitSample(ctx, time.Hour) }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.top(ctx, topInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestCPUTotal(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		want       uint64
		cpus       int
		bad        bool
	}{
		{"guest excluded", "cpu 100 0 100 800 0 0 0 0 50 0\ncpu0 1\ncpu1 1", 1000, 2, false},
		{"bad counter", "cpu abc\ncpu0 1", 0, 0, true}, {"missing cpu", "intr 123", 0, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "stat"), tt.text)
			p := provider{proc: root}
			total, cpus, err := p.cpuTotal()
			if tt.bad {
				if err == nil {
					t.Fatal("accepted invalid CPU stats")
				}
				return
			}
			if err != nil || total != tt.want || cpus != tt.cpus {
				t.Fatalf("%d %d %v", total, cpus, err)
			}
		})
	}
}

func TestCPUSampling(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprint("pid reuse=", reuse), func(t *testing.T) {
			root := processFixture(t)
			p := provider{proc: root, pause: func(context.Context) error {
				writeFile(t, filepath.Join(root, "stat"), "cpu 150 0 100 850 0 0 0 0\ncpu0 1\ncpu1 1\n")
				start := uint64(100)
				if reuse {
					start++
				}
				writeFile(t, filepath.Join(root, "20", "stat"), statLine(20, "worker", 75, start, 20))
				return nil
			}}
			out, err := p.top(context.Background(), topInput{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[1])
			wantPID, wantCPU := "20", "50.0"
			if reuse {
				wantPID, wantCPU = "10", "0.0"
			}
			if fields[0] != wantPID || fields[2] != wantCPU {
				t.Fatal(out)
			}
		})
	}
}
func TestWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitSample(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
