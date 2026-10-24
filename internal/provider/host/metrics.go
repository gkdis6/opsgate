package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func humanBytes(n uint64) string {
	v := float64(n)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}
func readText(root, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, name))
	return strings.TrimSpace(string(b)), err
}
func (p *provider) overview(ctx context.Context) (string, error) {
	var b strings.Builder
	for _, item := range []struct{ label, file string }{{"hostname", "sys/kernel/hostname"}, {"kernel", "sys/kernel/osrelease"}, {"load averages", "loadavg"}} {
		s, err := readText(p.proc, item.file)
		if err != nil {
			return "", err
		}
		if item.label == "load averages" {
			f := strings.Fields(s)
			if len(f) >= 3 {
				s = strings.Join(f[:3], " ")
			}
		}
		fmt.Fprintf(&b, "%s: %s\n", item.label, s)
	}
	release, err := readText(p.etc, "os-release")
	if err != nil {
		return "", err
	}
	osName := "unknown"
	for _, line := range strings.Split(release, "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			osName = strings.Trim(v, "\"'")
		}
	}
	fmt.Fprintf(&b, "OS: %s\n", osName)
	uptime, err := readText(p.proc, "uptime")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(uptime)
	if len(fields) == 0 {
		return "", fmt.Errorf("invalid uptime")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "uptime: %s\n", (time.Duration(seconds) * time.Second).String())
	_, cpus, err := p.cpuTotal()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "CPUs: %d\n", cpus)
	mem, err := readText(p.proc, "meminfo")
	if err != nil {
		return "", err
	}
	m := map[string]uint64{}
	for _, line := range strings.Split(mem, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			n, e := strconv.ParseUint(f[1], 10, 64)
			if e == nil {
				m[strings.TrimSuffix(f[0], ":")] = n * 1024
			}
		}
	}
	used := m["MemTotal"] - min(m["MemAvailable"], m["MemTotal"])
	swap := m["SwapTotal"] - min(m["SwapFree"], m["SwapTotal"])
	fmt.Fprintf(&b, "memory: total %s; available %s; used %.1f%%\nswap: total %s; free %s; used %s (%.1f%%)\n", humanBytes(m["MemTotal"]), humanBytes(m["MemAvailable"]), percent(used, m["MemTotal"]), humanBytes(m["SwapTotal"]), humanBytes(m["SwapFree"]), humanBytes(swap), percent(swap, m["SwapTotal"]))
	for _, path := range p.cfg.DiskPaths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			fmt.Fprintf(&b, "disk %s: %v\n", path, err)
			continue
		}
		size := st.Blocks * uint64(st.Bsize)
		used := (st.Blocks - st.Bfree) * uint64(st.Bsize)
		avail := st.Bavail * uint64(st.Bsize)
		fmt.Fprintf(&b, "disk %s: size %s; used %s; avail %s; use %.1f%%; inode use %.1f%%\n", path, humanBytes(size), humanBytes(used), humanBytes(avail), percent(used, used+avail), percent(st.Files-st.Ffree, st.Files))
	}
	units, err := p.units(ctx)
	if err != nil {
		fmt.Fprintf(&b, "units: unavailable: %v\n", err)
		return b.String(), nil
	}
	rows, err := p.properties(ctx, units, false)
	if err != nil {
		fmt.Fprintf(&b, "units: unavailable: %v\n", err)
		return b.String(), nil
	}
	active := 0
	var failed []string
	for _, row := range rows {
		if row["ActiveState"] == "active" {
			active++
		}
		if row["ActiveState"] == "failed" {
			failed = append(failed, row["Id"])
		}
	}
	fmt.Fprintf(&b, "units: %d active; %d failed; failed units: %s\n", active, len(failed), strings.Join(failed, ", "))
	return b.String(), nil
}
