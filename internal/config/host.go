package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"time"
)

// Host is optional and never enabled by a deployment mode.
type Host struct {
	Enabled bool        `yaml:"enabled"`
	Systemd HostSystemd `yaml:"systemd"`
	Docker  HostDocker  `yaml:"docker"`
	// PollInterval bounds watch API and process usage.
	PollInterval time.Duration `yaml:"poll_interval"`
}

type HostSystemd struct {
	Units   []string `yaml:"units"`
	Restart []string `yaml:"restart"`
}

type HostDocker struct {
	Endpoint   string   `yaml:"endpoint"`
	Containers []string `yaml:"containers"`
	Restart    []string `yaml:"restart"`
}

func (h Host) validate() error {
	if h.PollInterval < 10*time.Second || h.PollInterval > 5*time.Minute {
		return fmt.Errorf("providers.host.poll_interval must be between 10s and 5m")
	}
	for _, list := range []struct {
		name     string
		patterns []string
		compose  bool
	}{
		{"systemd.units", h.Systemd.Units, false}, {"systemd.restart", h.Systemd.Restart, false},
		{"docker.containers", h.Docker.Containers, true}, {"docker.restart", h.Docker.Restart, true},
	} {
		if len(list.patterns) > 256 {
			return fmt.Errorf("providers.host.%s: at most 256 patterns", list.name)
		}
		for _, p := range list.patterns {
			if len(p) == 0 || len(p) > 256 || strings.HasPrefix(p, "-") || strings.ContainsAny(p, "\\\r\n\x00 \t") {
				return fmt.Errorf("providers.host.%s: invalid pattern", list.name)
			}
			if _, err := path.Match(p, ""); err != nil {
				return fmt.Errorf("providers.host.%s: invalid glob", list.name)
			}
			if strings.HasPrefix(p, "compose:") && list.compose {
				parts := strings.Split(strings.TrimPrefix(p, "compose:"), "/")
				if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
					return fmt.Errorf("providers.host.%s: use compose:project/service", list.name)
				}
			} else if strings.Contains(p, "/") || list.compose && strings.Contains(p, ":") {
				return fmt.Errorf("providers.host.%s: invalid pattern", list.name)
			}
		}
	}
	if h.Docker.Endpoint != "" {
		u, err := url.Parse(h.Docker.Endpoint)
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return fmt.Errorf("providers.host.docker.endpoint: invalid endpoint")
		}
		unix := u.Scheme == "unix" && u.Host == "" && strings.HasPrefix(u.Path, "/") && path.Clean(u.Path) == u.Path && u.Path != "/"
		ip := net.ParseIP(u.Hostname())
		http := u.Scheme == "http" && ip != nil && ip.IsLoopback() && (u.Path == "" || u.Path == "/") && u.Host != ""
		if !unix && !http {
			return fmt.Errorf("providers.host.docker.endpoint must be unix:///absolute/socket or http://loopback-IP:port")
		}
	}
	return nil
}
