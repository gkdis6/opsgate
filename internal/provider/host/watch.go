package host

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
)

// Hints are fixed strings so host text can never pose as advice.
const (
	hintUnit      = "Check the unit's journal (host_unit_logs) for the cause before restarting it again."
	hintContainer = "Check the container's logs (host_container_logs) for the cause before restarting it again."
)

// Watch polls the allowlisted units and containers and emits an alert when
// one starts failing or restarts. The first poll only records a baseline, so
// starting watch does not repeat old problems.
func Watch(ctx context.Context, cfg config.Host, emit func(alert.Alert)) error {
	p, err := newProvider(cfg)
	if err != nil {
		return err
	}
	if !p.systemdEnabled() && !p.dockerEnabled() {
		return fmt.Errorf("providers.host enables neither systemd units nor Docker containers")
	}
	w := &hostWatcher{p: p, emit: emit, now: time.Now}
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		w.poll(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type hostWatcher struct {
	p          *provider
	emit       func(alert.Alert)
	now        func() time.Time
	units      map[string]unitState
	containers map[string]containerState
	failing    bool
}

func (w *hostWatcher) poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok := true
	if w.p.systemdEnabled() {
		units, err := w.p.units(ctx)
		if err == nil {
			w.compareUnits(units)
		}
		ok = ok && w.report("systemd", err)
	}
	if w.p.dockerEnabled() {
		containers, err := w.p.containers(ctx)
		if err == nil {
			w.compareContainers(containers)
		}
		ok = ok && w.report("docker", err)
	}
	if ok && w.failing {
		log.Print("host watch: polling recovered")
	}
	w.failing = !ok
}

// report logs a polling failure once until polling recovers.
func (w *hostWatcher) report(source string, err error) bool {
	if err != nil && ctxAlive(err) && !w.failing {
		log.Printf("host watch: %s poll failed: %v", source, err)
	}
	return err == nil
}

func ctxAlive(err error) bool { return !strings.Contains(err.Error(), "context canceled") }

func (w *hostWatcher) compareUnits(units []unitState) {
	next := make(map[string]unitState, len(units))
	for _, u := range units {
		next[u.Unit] = u
		old, seen := w.units[u.Unit]
		if w.units == nil || !seen {
			continue
		}
		switch {
		case u.failed() && !old.failed():
			w.alert("Unit", u.Unit, "UnitFailed", fmt.Sprintf("systemd unit %s failed (%s/%s, result %s)", u.Unit, token(u.Active), token(u.Sub), token(u.Result)), hintUnit)
		case u.NRestarts > old.NRestarts:
			w.alert("Unit", u.Unit, "UnitRestarted", fmt.Sprintf("systemd unit %s restarted %d time(s); %d restarts in total", u.Unit, u.NRestarts-old.NRestarts, u.NRestarts), hintUnit)
		}
	}
	w.units = next
}

func (w *hostWatcher) compareContainers(containers []containerState) {
	next := make(map[string]containerState, len(containers))
	for _, c := range containers {
		next[c.Container] = c
		old, seen := w.containers[c.Container]
		// A recreated container starts a new history.
		if w.containers == nil || !seen || old.ID != c.ID {
			continue
		}
		switch {
		case c.Health == "unhealthy" && old.Health != "unhealthy":
			w.alert("Container", c.Container, "ContainerUnhealthy", fmt.Sprintf("Docker container %s is unhealthy (%s)", c.Container, token(c.State)), hintContainer)
		case c.State == "exited" && c.ExitCode != 0 && old.State != "exited":
			w.alert("Container", c.Container, "ContainerExited", fmt.Sprintf("Docker container %s exited with code %d", c.Container, c.ExitCode), hintContainer)
		case c.Restarts > old.Restarts:
			w.alert("Container", c.Container, "ContainerRestarted", fmt.Sprintf("Docker container %s restarted %d time(s); %d restarts in total", c.Container, c.Restarts-old.Restarts, c.Restarts), hintContainer)
		}
	}
	w.containers = next
}

func (w *hostWatcher) alert(kind, name, reason, summary, hint string) {
	w.emit(alert.Alert{Time: w.now(), Cluster: alert.HostCluster, Severity: alert.Warning, Kind: kind, Namespace: alert.HostCluster,
		Name: name, Reason: reason, Summary: summary, Hint: hint, Key: "host/" + kind + "/" + name + "/" + reason})
}

// token keeps a host-reported state word only if it looks like one.
func token(s string) string {
	if s == "" || len(s) > 32 || strings.IndexFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') }) >= 0 {
		return "unknown"
	}
	return s
}
