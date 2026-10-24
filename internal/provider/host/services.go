package host

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var dayDuration = regexp.MustCompile(`(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)d`)

const basicProperties = "Id,LoadState,ActiveState,SubState,ActiveEnterTimestamp,NRestarts,MainPID,MemoryCurrent"
const detailProperties = basicProperties + ",Description,FragmentPath,ExecMainStartTimestamp,ExecMainStatus,Result,ActiveExitTimestamp"

func (p *provider) units(ctx context.Context) ([]string, error) {
	found := map[string]bool{}
	globs := false
	for _, pattern := range p.cfg.Units {
		if strings.ContainsAny(pattern, "*?[") {
			globs = true
			continue
		}
		u, err := p.allowed(pattern)
		if err != nil {
			return nil, err
		}
		found[u] = true
	}
	if globs {
		out, err := p.run(ctx, "systemctl", "list-units", "--all", "--plain", "--no-legend", "--no-pager", "--full")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[1] == "not-found" {
				continue
			}
			if u, err := p.allowed(fields[0]); err == nil {
				found[u] = true
			}
		}
	}
	units := make([]string, 0, len(found))
	for u := range found {
		units = append(units, u)
	}
	sort.Strings(units)
	return units, nil
}
func parseProperties(s string) []map[string]string {
	var result []map[string]string
	current := map[string]string{}
	flush := func() {
		if len(current) > 0 {
			result = append(result, current)
			current = map[string]string{}
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			current[k] = v
		}
	}
	flush()
	return result
}
func (p *provider) properties(ctx context.Context, units []string, detail bool) ([]map[string]string, error) {
	if len(units) == 0 {
		return nil, nil
	}
	props := basicProperties
	if detail {
		props = detailProperties
	}
	args := []string{"show", "--no-pager", "--property=" + props, "--"}
	args = append(args, units...)
	out, err := p.run(ctx, "systemctl", args...)
	if err != nil {
		return nil, err
	}
	return parseProperties(out), nil
}
func (p *provider) services(ctx context.Context) (string, error) {
	units, err := p.units(ctx)
	if err != nil {
		return "", err
	}
	rows, err := p.properties(ctx, units, false)
	if err != nil {
		return "", err
	}
	if len(units) == 0 {
		return "No allowed units.", nil
	}
	var b strings.Builder
	for _, row := range rows {
		writeProperties(&b, row, false)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String()), nil
}
func writeProperties(b *strings.Builder, row map[string]string, detail bool) {
	props := basicProperties
	if detail {
		props = detailProperties
	}
	for _, key := range strings.Split(props, ",") {
		v := row[key]
		if key == "MemoryCurrent" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil && n != ^uint64(0) {
				v = humanBytes(n)
			}
		}
		fmt.Fprintf(b, "%s: %s\n", key, v)
	}
}
func (p *provider) status(ctx context.Context, raw string) (string, error) {
	unit, err := p.allowed(raw)
	if err != nil {
		return "", err
	}
	rows, err := p.properties(ctx, []string{unit}, true)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, row := range rows {
		writeProperties(&b, row, true)
	}
	return b.String(), nil
}
func absoluteSince(s string, now time.Time) (string, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(time.Local).Format("2006-01-02 15:04:05"), nil
	}
	if len(s) > 128 {
		return "", fmt.Errorf("since exceeds 128 bytes")
	}
	// Replace day components so compound durations retain ParseDuration's
	// syntax and overflow checks. A day means 24 hours, independent of DST.
	s = dayDuration.ReplaceAllStringFunc(s, func(component string) string {
		n, err := strconv.ParseFloat(strings.TrimSuffix(component, "d"), 64)
		if err != nil {
			return "invalid"
		}
		return strconv.FormatFloat(n*24, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return "", fmt.Errorf("invalid since: use a positive duration or RFC3339")
	}
	return now.Add(-d).In(time.Local).Format("2006-01-02 15:04:05"), nil
}
func (p *provider) logs(ctx context.Context, in logsInput) (string, error) {
	unit, err := p.allowed(in.Unit)
	if err != nil {
		return "", err
	}
	max := p.cfg.MaxLogLines
	if max <= 0 {
		max = 500
	}
	lines := in.Lines
	if lines == 0 {
		lines = 100
	}
	if lines < 1 {
		lines = 1
	}
	if lines > max {
		lines = max
	}
	if len(in.Grep) > 4096 {
		return "", fmt.Errorf("grep exceeds 4096 bytes")
	}
	scan := lines
	if in.Grep != "" {
		scan = max
	}
	args := []string{"-u", unit, "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(scan)}
	since := ""
	if in.Since != "" {
		since, err = absoluteSince(in.Since, p.now())
		if err != nil {
			return "", err
		}
		args = append(args, "--since", since)
	}
	if in.Priority != "" {
		valid := false
		for _, s := range []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug", "0", "1", "2", "3", "4", "5", "6", "7"} {
			if in.Priority == s {
				valid = true
			}
		}
		if !valid {
			return "", fmt.Errorf("invalid priority")
		}
		args = append(args, "-p", in.Priority)
	}
	out, err := p.run(ctx, "journalctl", args...)
	if err != nil {
		return "", err
	}
	var scanned, matched []string
	if out != "" {
		scanned = strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	}
	needle := strings.ToLower(in.Grep)
	for _, line := range scanned {
		if strings.Contains(strings.ToLower(line), needle) {
			matched = append(matched, line)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "unit: %s; lines: %d; scanned: %d; matched: %d; since: %q; priority: %q; grep: %q\n", unit, lines, len(scanned), len(matched), since, in.Priority, in.Grep)
	start := len(matched) - lines
	if start < 0 {
		start = 0
	}
	for _, line := range matched[start:] {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
