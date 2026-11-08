package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// fakeHost records argv and answers like systemctl/journalctl would.
type fakeHost struct {
	calls  [][]string
	units  string // list-units output
	show   map[string]string
	logs   string
	failOn string
}

func (f *fakeHost) run(_ context.Context, binary string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{binary}, args...))
	if f.failOn != "" && slices.Contains(args, f.failOn) {
		return "", errors.New("boom")
	}
	switch {
	case binary == systemctl && args[0] == "list-units":
		return f.units, nil
	case binary == systemctl && args[0] == "show":
		var blocks []string
		for _, name := range args[slices.Index(args, "--")+1:] {
			if b, ok := f.show[name]; ok {
				blocks = append(blocks, b)
			}
		}
		return strings.Join(blocks, "\n\n") + "\n", nil
	case binary == journalctl:
		return f.logs, nil
	}
	return "", nil
}

func unitBlock(name, active, sub, result string, restarts int) string {
	return fmt.Sprintf("Id=%s\nLoadState=loaded\nActiveState=%s\nSubState=%s\nResult=%s\nNRestarts=%d\nStateChangeTimestamp=Sun 2026-11-08 10:00:00 UTC", name, active, sub, result, restarts)
}

func systemdProvider(f *fakeHost) *provider {
	cfg := config.Host{Enabled: true, Systemd: config.HostSystemd{Units: []string{"app-*.service", "nginx.service"}, Restart: []string{"app-*.service"}}}
	return &provider{cfg: cfg, run: f.run}
}

func TestUnitsFilterAllowlistAndParse(t *testing.T) {
	f := &fakeHost{
		units: "app-web.service loaded active running Web\nsshd.service loaded active running SSH\n-evil.service loaded active running x\nnginx.service loaded failed failed Nginx\n",
		show: map[string]string{
			"app-web.service": unitBlock("app-web.service", "active", "running", "success", 2),
			"nginx.service":   unitBlock("nginx.service", "failed", "failed", "exit-code", 0),
		},
	}
	units, err := systemdProvider(f).units(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].Unit != "app-web.service" || units[0].NRestarts != 2 || units[0].failed() || !units[1].failed() {
		t.Fatalf("%+v", units)
	}
	show := f.calls[1]
	if !slices.Equal(show[len(show)-3:], []string{"--", "app-web.service", "nginx.service"}) {
		t.Fatalf("show argv %v", show)
	}
}

func TestRestartUnitPolicyAndArgv(t *testing.T) {
	f := &fakeHost{show: map[string]string{"app-web.service": unitBlock("app-web.service", "failed", "failed", "exit-code", 1), "nginx.service": unitBlock("nginx.service", "active", "running", "success", 0)}}
	p := systemdProvider(f)
	ctx := context.Background()
	for _, name := range []string{"nginx.service", "sshd.service", "app-web.service; rm -rf /", "--help", "app-web"} {
		if _, err := p.planRestartUnit(ctx, tool.Caller{}, restartUnitInput{Unit: name}); !errors.Is(err, tool.ErrDenied) {
			t.Fatalf("%q planned: %v", name, err)
		}
	}
	in := restartUnitInput{Unit: "app-web.service"}
	plan, err := p.planRestartUnit(ctx, tool.Caller{}, in)
	if err != nil || !strings.Contains(plan.Summary, "app-web.service") || plan.Undo != nil {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := p.applyRestartUnit(ctx, tool.Caller{}, in, plan); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if !slices.Equal(last, []string{systemctl, "--no-ask-password", "restart", "--", "app-web.service"}) {
		t.Fatalf("restart argv %v", last)
	}
}

func TestUnitLogsMaskAndBound(t *testing.T) {
	var logs strings.Builder
	for i := range 200 {
		fmt.Fprintf(&logs, "line %d\n", i)
	}
	logs.WriteString("password=hunter2\n")
	f := &fakeHost{logs: logs.String()}
	out, err := systemdProvider(f).unitLogs(context.Background(), tool.Caller{}, unitLogsInput{Unit: "nginx.service", Lines: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hunter2") || strings.Count(out, "\n") != 3 || !strings.Contains(out, "line 199") {
		t.Fatalf("%q", out)
	}
	if !slices.Contains(f.calls[0], "--unit=nginx.service") || !slices.Contains(f.calls[0], "--lines=53") {
		t.Fatalf("argv %v", f.calls[0])
	}
	if _, err := systemdProvider(f).unitLogs(context.Background(), tool.Caller{}, unitLogsInput{Unit: "sshd.service"}); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("unlisted unit: %v", err)
	}
}

// fakeDocker answers Docker Engine API paths without a socket.
type fakeDocker struct {
	containers map[string]string // name -> inspect JSON
	list       string
	logs       []byte
	requests   []string
}

func (f *fakeDocker) RoundTrip(r *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	status, body := http.StatusOK, ""
	switch {
	case r.URL.Path == "/containers/json":
		body = f.list
	case strings.HasSuffix(r.URL.Path, "/json"):
		ref := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
		var ok bool
		if body, ok = f.containers[ref]; !ok {
			status = http.StatusNotFound
		}
	case strings.HasSuffix(r.URL.Path, "/logs"):
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(f.logs)), Header: http.Header{}}, nil
	case strings.HasSuffix(r.URL.Path, "/restart"):
		status = http.StatusNoContent
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func inspectJSON(id, name, state string, exit, restarts int, health, project, service string) string {
	h := "null"
	if health != "" {
		h = fmt.Sprintf(`{"Status":%q}`, health)
	}
	return fmt.Sprintf(`{"Id":%q,"Name":"/%s","RestartCount":%d,"State":{"Status":%q,"ExitCode":%d,"Health":%s},"Config":{"Image":"img:1","Tty":false,"Labels":{"com.docker.compose.project":%q,"com.docker.compose.service":%q}}}`,
		id, name, restarts, state, exit, h, project, service)
}

func dockerProvider(f *fakeDocker) *provider {
	cfg := config.Host{Enabled: true, Docker: config.HostDocker{Endpoint: "unix:///run/proxy.sock", Containers: []string{"web", "compose:shop/*"}, Restart: []string{"compose:shop/*"}}}
	return &provider{cfg: cfg, docker: &dockerClient{http: &http.Client{Transport: f}, base: "http://docker"}}
}

var (
	idA = strings.Repeat("a", 64)
	idB = strings.Repeat("b", 64)
	idC = strings.Repeat("c", 64)
)

func TestContainersAllowlist(t *testing.T) {
	f := &fakeDocker{
		list: fmt.Sprintf(`[{"Id":%q,"Names":["/web"]},{"Id":%q,"Names":["/shop-api-1"],"Labels":{"com.docker.compose.project":"shop","com.docker.compose.service":"api"}},{"Id":%q,"Names":["/db"]}]`, idA, idB, idC),
		containers: map[string]string{
			idA: inspectJSON(idA, "web", "running", 0, 0, "healthy", "", ""),
			idB: inspectJSON(idB, "shop-api-1", "running", 0, 3, "unhealthy", "shop", "api"),
			idC: inspectJSON(idC, "db", "running", 0, 0, "", "", ""),
		},
	}
	got, err := dockerProvider(f).containers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Container != "shop-api-1" || !got[0].failed() || got[1].Container != "web" || got[1].failed() {
		t.Fatalf("%+v", got)
	}
	for _, r := range f.requests {
		if strings.Contains(r, idC) {
			t.Fatal("inspected a container outside the allowlist")
		}
	}
}

func TestRestartContainerBindsID(t *testing.T) {
	f := &fakeDocker{containers: map[string]string{
		"shop-api-1": inspectJSON(idB, "shop-api-1", "running", 0, 3, "unhealthy", "shop", "api"),
		"web":        inspectJSON(idA, "web", "running", 0, 0, "", "", ""),
		"shop":       inspectJSON(idB, "shop-api-1", "running", 0, 3, "", "shop", "api"),
	}}
	p := dockerProvider(f)
	ctx := context.Background()
	for _, name := range []string{"web", "shop", "../x", "missing"} {
		if _, err := p.planRestartContainer(ctx, tool.Caller{}, restartContainerInput{Container: name}); err == nil {
			t.Fatalf("%q planned", name)
		}
	}
	in := restartContainerInput{Container: "shop-api-1"}
	plan, err := p.planRestartContainer(ctx, tool.Caller{}, in)
	if err != nil || !strings.Contains(plan.Summary, idB) {
		t.Fatalf("%+v %v", plan, err)
	}
	f.containers["shop-api-1"] = inspectJSON(idC, "shop-api-1", "running", 0, 0, "", "shop", "api")
	if _, err := p.applyRestartContainer(ctx, tool.Caller{}, in, plan); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("replaced container restarted: %v", err)
	}
	f.containers["shop-api-1"] = inspectJSON(idB, "shop-api-1", "running", 0, 3, "", "shop", "api")
	if _, err := p.applyRestartContainer(ctx, tool.Caller{}, in, plan); err != nil {
		t.Fatal(err)
	}
	if last := f.requests[len(f.requests)-1]; last != "POST /containers/"+idB+"/restart?t=10" {
		t.Fatalf("restart request %q", last)
	}
}

func frame(stream byte, text string) []byte {
	b := make([]byte, 8, 8+len(text))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(text)))
	return append(b, text...)
}

func TestContainerLogsDemuxMask(t *testing.T) {
	f := &fakeDocker{containers: map[string]string{"web": inspectJSON(idA, "web", "running", 0, 0, "", "", "")},
		logs: append(frame(1, "hello\n"), frame(2, "token=abcdefghijklmnop\n")...)}
	out, err := dockerProvider(f).containerLogs(context.Background(), tool.Caller{}, containerInput{Container: "web"})
	if err != nil || !strings.Contains(out, "hello") || strings.Contains(out, "abcdefghijklmnop") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := demux([]byte{1, 0, 0, 0, 0, 0, 0, 9, 'x'}); err == nil {
		t.Fatal("truncated frame accepted")
	}
}

func TestOptionsRules(t *testing.T) {
	f := &fakeHost{show: map[string]string{
		"app-web.service": unitBlock("app-web.service", "failed", "failed", "exit-code", 0),
		"nginx.service":   unitBlock("nginx.service", "failed", "failed", "exit-code", 0),
		"app-ok.service":  unitBlock("app-ok.service", "active", "running", "success", 0),
	}}
	p := systemdProvider(f)
	p.cfg.Systemd.Units = append(p.cfg.Systemd.Units, "app-ok.service")
	ctx := context.Background()
	out, err := p.options(ctx, "unit", "app-web.service")
	if err != nil || len(out.Actions) != 1 || out.Actions[0].Tool != "host_restart_unit" || out.Actions[0].URL != "/approve/action/host_restart_unit?source=rule&unit=app-web.service" {
		t.Fatalf("%+v %v", out, err)
	}
	// Failed but not in the restart allowlist, and healthy: no choice.
	for _, name := range []string{"nginx.service", "app-ok.service"} {
		if out, err := p.options(ctx, "unit", name); err != nil || len(out.Actions) != 0 || out.Hint == "" {
			t.Fatalf("%s: %+v %v", name, out, err)
		}
	}
	if _, err := p.options(ctx, "unit", "sshd.service"); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("unlisted unit: %v", err)
	}
	if _, err := p.options(ctx, "container", "web"); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("docker disabled: %v", err)
	}
}

func TestWatchDetectsChangesAfterBaseline(t *testing.T) {
	var got []alert.Alert
	w := &hostWatcher{emit: func(a alert.Alert) { got = append(got, a) }, now: time.Now}
	w.compareUnits([]unitState{{Unit: "a.service", Active: "failed", Result: "exit-code"}, {Unit: "b.service", Active: "active", Result: "success"}})
	if len(got) != 0 {
		t.Fatalf("baseline alerted: %+v", got)
	}
	w.compareUnits([]unitState{{Unit: "a.service", Active: "failed", Result: "exit-code"}, {Unit: "b.service", Active: "failed", Sub: "failed", Result: "exit-code"}})
	w.compareUnits([]unitState{{Unit: "a.service", Active: "active", Result: "success", NRestarts: 2}, {Unit: "b.service", Active: "failed", Result: "exit-code"}})
	if len(got) != 2 || got[0].Name != "b.service" || got[0].Reason != "UnitFailed" || got[1].Reason != "UnitRestarted" {
		t.Fatalf("%+v", got)
	}
	if alert.HostKind(got[0]) != "unit" || got[0].Severity != alert.Warning {
		t.Fatalf("not a host alert: %+v", got[0])
	}

	got = nil
	w.compareContainers([]containerState{{Container: "web", ID: idA, State: "running"}, {Container: "api", ID: idB, State: "running"}})
	w.compareContainers([]containerState{{Container: "web", ID: idA, State: "running", Health: "unhealthy"}, {Container: "api", ID: idC, State: "exited", ExitCode: 1}})
	w.compareContainers([]containerState{{Container: "web", ID: idA, State: "exited", ExitCode: 137, Health: "unhealthy"}, {Container: "api", ID: idC, State: "running", Restarts: 1}})
	reasons := []string{}
	for _, a := range got {
		reasons = append(reasons, a.Name+"/"+a.Reason)
	}
	// api was recreated (new ID), so its first exit is a new baseline.
	if !slices.Equal(reasons, []string{"web/ContainerUnhealthy", "web/ContainerExited", "api/ContainerRestarted"}) {
		t.Fatalf("%v", reasons)
	}
	if token("running") != "running" || token("bad state!") != "unknown" {
		t.Fatal("token filter")
	}
}
