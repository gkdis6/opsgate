package host

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/hosttarget"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

const (
	maxContainers  = 100
	maxDockerBody  = 4 << 20
	composeProject = "com.docker.compose.project"
	composeService = "com.docker.compose.service"
)

// dockerClient talks to the Docker Engine API, or to a socket proxy that
// allows only the calls below, over a unix socket or loopback HTTP.
type dockerClient struct {
	http *http.Client
	base string
}

func newDockerClient(endpoint string) (*dockerClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	base := strings.TrimRight(endpoint, "/")
	if u.Scheme == "unix" {
		socket := u.Path
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://docker"
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &dockerClient{http: client, base: base}, nil
}

func (d *dockerClient) do(ctx context.Context, method, apiPath string, query url.Values, want int) ([]byte, error) {
	target := d.base + apiPath
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	res, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker API unavailable")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxDockerBody+1))
	if err != nil {
		return nil, fmt.Errorf("docker API response unreadable")
	}
	if len(body) > maxDockerBody {
		return nil, fmt.Errorf("docker API response exceeded %d MiB", maxDockerBody>>20)
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, tool.Denied("container not found")
	}
	if res.StatusCode != want {
		return nil, fmt.Errorf("docker API returned %d", res.StatusCode)
	}
	return body, nil
}

type containerState struct {
	Container string `json:"container"`
	ID        string `json:"id"`
	Image     string `json:"image"`
	State     string `json:"state"`
	Health    string `json:"health,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Restarts  int    `json:"restarts"`
	Project   string `json:"compose_project,omitempty"`
	Service   string `json:"compose_service,omitempty"`
	tty       bool
}

// failed reports whether the container is in a state a restart may fix.
func (c containerState) failed() bool {
	return c.Health == "unhealthy" || c.State == "restarting" || c.State == "exited" && c.ExitCode != 0
}

type inspectResponse struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status   string `json:"Status"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Tty    bool              `json:"Tty"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (d *dockerClient) inspect(ctx context.Context, ref string) (containerState, error) {
	body, err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(ref)+"/json", nil, http.StatusOK)
	if err != nil {
		return containerState{}, err
	}
	var r inspectResponse
	if json.Unmarshal(body, &r) != nil || len(r.ID) != 64 {
		return containerState{}, fmt.Errorf("unexpected docker inspect response")
	}
	c := containerState{Container: strings.TrimPrefix(r.Name, "/"), ID: r.ID, Image: r.Config.Image, State: r.State.Status, ExitCode: r.State.ExitCode,
		Restarts: r.RestartCount, Project: r.Config.Labels[composeProject], Service: r.Config.Labels[composeService], tty: r.Config.Tty}
	if r.State.Health != nil {
		c.Health = r.State.Health.Status
	}
	return c, nil
}

// matchContainer accepts a container name glob, or compose:<project>/<service>.
func matchContainer(patterns []string, c containerState) bool {
	for _, p := range patterns {
		if compose, ok := strings.CutPrefix(p, "compose:"); ok {
			if c.Project != "" && c.Service != "" && !strings.Contains(c.Project+c.Service, "/") {
				if ok, _ := path.Match(compose, c.Project+"/"+c.Service); ok {
					return true
				}
			}
			continue
		}
		if ok, _ := path.Match(p, c.Container); ok {
			return true
		}
	}
	return false
}

func (p *provider) containers(ctx context.Context) ([]containerState, error) {
	body, err := p.docker.do(ctx, http.MethodGet, "/containers/json", url.Values{"all": {"true"}}, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var list []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if json.Unmarshal(body, &list) != nil {
		return nil, fmt.Errorf("unexpected docker list response")
	}
	var out []containerState
	for _, item := range list {
		if len(item.Names) == 0 {
			continue
		}
		candidate := containerState{Container: strings.TrimPrefix(item.Names[0], "/"), Project: item.Labels[composeProject], Service: item.Labels[composeService]}
		if !hosttarget.Valid("container", candidate.Container) || !matchContainer(p.cfg.Docker.Containers, candidate) {
			continue
		}
		if len(out) == maxContainers {
			return nil, tool.Denied("more than %d allowlisted containers; narrow providers.host.docker.containers", maxContainers)
		}
		c, err := p.docker.inspect(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b containerState) int { return strings.Compare(a.Container, b.Container) })
	return out, nil
}

// container resolves an allowlisted container by name.
func (p *provider) container(ctx context.Context, name string, restart bool) (containerState, error) {
	if !hosttarget.Valid("container", name) {
		return containerState{}, tool.Denied("invalid container name")
	}
	c, err := p.docker.inspect(ctx, name)
	if err != nil {
		return containerState{}, err
	}
	// Docker also resolves ID prefixes; insist on the exact name.
	if c.Container != name || !matchContainer(p.cfg.Docker.Containers, c) || restart && !matchContainer(p.cfg.Docker.Restart, c) {
		return containerState{}, tool.Denied("container %s is not allowed", name)
	}
	return c, nil
}

type containerInput struct {
	Container string `json:"container" jsonschema:"Required allowlisted container name"`
	Lines     int    `json:"lines,omitempty" jsonschema:"Log lines to return; default 100, maximum 500"`
}

type restartContainerInput struct {
	tool.Approval
	Container string `json:"container" jsonschema:"Required container name allowed by providers.host.docker.restart"`
}

func (p *provider) containerLogs(ctx context.Context, _ tool.Caller, in containerInput) (string, error) {
	c, err := p.container(ctx, in.Container, false)
	if err != nil {
		return "", err
	}
	lines := boundedLines(in.Lines)
	body, err := p.docker.do(ctx, http.MethodGet, "/containers/"+c.ID+"/logs", url.Values{"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}, "tail": {strconv.Itoa(lines + 50)}}, http.StatusOK)
	if err != nil {
		return "", err
	}
	text := string(body)
	if !c.tty {
		if text, err = demux(body); err != nil {
			return "", err
		}
	}
	return lastLines(redact.String(text), lines), nil
}

// demux joins Docker's multiplexed stdout/stderr frames.
func demux(b []byte) (string, error) {
	var out bytes.Buffer
	for len(b) > 0 {
		if len(b) < 8 || b[0] > 2 {
			return "", fmt.Errorf("unexpected docker log stream")
		}
		size := binary.BigEndian.Uint32(b[4:8])
		if uint64(size) > uint64(len(b)-8) {
			return "", fmt.Errorf("truncated docker log stream")
		}
		out.Write(b[8 : 8+size])
		b = b[8+size:]
	}
	return out.String(), nil
}

func (p *provider) planRestartContainer(ctx context.Context, _ tool.Caller, in restartContainerInput) (tool.Plan, error) {
	c, err := p.container(ctx, in.Container, true)
	if err != nil {
		return tool.Plan{}, err
	}
	summary := fmt.Sprintf("Restart Docker container %s (ID %s, image %q) on this host with a 10s stop timeout.", c.Container, c.ID, c.Image)
	return tool.Plan{Summary: summary, Target: "host", Data: c.ID}, nil
}

func (p *provider) applyRestartContainer(ctx context.Context, _ tool.Caller, in restartContainerInput, plan tool.Plan) (string, error) {
	id, _ := plan.Data.(string)
	c, err := p.container(ctx, in.Container, true)
	if err != nil {
		return "", err
	}
	// A recreated container has a new ID; it was not the one approved.
	if id == "" || c.ID != id {
		return "", tool.Denied("container %s was replaced since the plan; review a new plan", in.Container)
	}
	if _, err := p.docker.do(ctx, http.MethodPost, "/containers/"+id+"/restart", url.Values{"t": {"10"}}, http.StatusNoContent); err != nil {
		return "", err
	}
	return fmt.Sprintf("Restarted Docker container %s; check host_containers for its state.", c.Container), nil
}
