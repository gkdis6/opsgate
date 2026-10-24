package host

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

type process struct {
	pid               int
	uid, comm, state  string
	ticks, rss, start uint64
	cpu               float64
}

var users sync.Map

func username(uid string) string {
	if v, ok := users.Load(uid); ok {
		return v.(string)
	}
	name := uid
	if u, err := user.LookupId(uid); err == nil {
		name = u.Username
	}
	users.Store(uid, name)
	return name
}
func (p *provider) cpuTotal() (uint64, int, error) {
	s, err := readText(p.proc, "stat")
	if err != nil {
		return 0, 0, err
	}
	var total uint64
	count := 0
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "cpu" {
			// Guest times are already included in user and nice.
			for i := 1; i < len(f) && i <= 8; i++ {
				n, err := strconv.ParseUint(f[i], 10, 64)
				if err != nil {
					return 0, 0, fmt.Errorf("invalid CPU stat: %w", err)
				}
				total += n
			}
		} else if strings.HasPrefix(f[0], "cpu") {
			if _, err := strconv.Atoi(f[0][3:]); err == nil {
				count++
			}
		}
	}
	if count == 0 || total == 0 {
		return 0, 0, fmt.Errorf("missing CPU counters")
	}
	return total, count, nil
}
func parseStat(s string) (process, error) {
	open := strings.Index(s, "(")
	close := strings.LastIndex(s, ")")
	if open < 1 || close <= open {
		return process{}, fmt.Errorf("invalid process stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil || pid <= 0 {
		return process{}, fmt.Errorf("invalid process PID")
	}
	f := strings.Fields(s[close+1:])
	if len(f) < 22 || len(f[0]) != 1 {
		return process{}, fmt.Errorf("short process stat")
	}
	values := make([]uint64, 4)
	for i, index := range []int{11, 12, 19, 21} {
		n, err := strconv.ParseUint(f[index], 10, 64)
		if err != nil {
			return process{}, err
		}
		values[i] = n
	}
	return process{pid: pid, state: f[0], ticks: values[0] + values[1], start: values[2], rss: values[3] * uint64(os.Getpagesize())}, nil
}
func (p *provider) processes(ctx context.Context) (map[int]process, error) {
	entries, err := os.ReadDir(p.proc)
	if err != nil {
		return nil, err
	}
	result := map[int]process{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		dir := filepath.Join(p.proc, entry.Name())
		s, err := readText(dir, "stat")
		if err != nil {
			continue
		}
		v, err := parseStat(s)
		if err != nil || v.pid != pid {
			continue
		}
		v.comm, err = readText(dir, "comm")
		if err != nil {
			continue
		}
		status, err := readText(dir, "status")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "Uid:") {
				f := strings.Fields(line)
				if len(f) > 1 {
					if _, err := strconv.ParseUint(f[1], 10, 32); err == nil {
						v.uid = f[1]
					}
				}
				break
			}
		}
		if v.uid == "" {
			continue
		}
		result[pid] = v
	}
	return result, nil
}
func (p *provider) top(ctx context.Context, in topInput) (string, error) {
	if in.Sort == "" {
		in.Sort = "cpu"
	}
	if in.Sort != "cpu" && in.Sort != "mem" {
		return "", fmt.Errorf("sort must be cpu or mem")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 15
	}
	limit = max(1, min(limit, 50))
	total, _, err := p.cpuTotal()
	if err != nil {
		return "", err
	}
	before, err := p.processes(ctx)
	if err != nil {
		return "", err
	}
	if err := p.pause(ctx); err != nil {
		return "", err
	}
	end, cpus, err := p.cpuTotal()
	if err != nil {
		return "", err
	}
	after, err := p.processes(ctx)
	if err != nil {
		return "", err
	}
	rows := make([]process, 0, len(after))
	for pid, v := range after {
		if old, ok := before[pid]; ok && old.start == v.start && v.ticks >= old.ticks && end > total {
			v.cpu = 100 * float64(v.ticks-old.ticks) * float64(cpus) / float64(end-total)
		}
		rows = append(rows, v)
	}
	sort.Slice(rows, func(i, j int) bool {
		if in.Sort == "mem" {
			if rows[i].rss != rows[j].rss {
				return rows[i].rss > rows[j].rss
			}
		} else if rows[i].cpu != rows[j].cpu {
			return rows[i].cpu > rows[j].cpu
		}
		return rows[i].pid < rows[j].pid
	})
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tUSER\tCPU%\tRSS\tSTATE\tCOMMAND")
	for _, v := range rows[:min(limit, len(rows))] {
		fmt.Fprintf(w, "%d\t%s\t%.1f\t%s\t%s\t%s\n", v.pid, username(v.uid), v.cpu, humanBytes(v.rss), v.state, cleanCommand(v.comm))
	}
	_ = w.Flush()
	return b.String(), nil
}
func cleanCommand(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}

func waitSample(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
