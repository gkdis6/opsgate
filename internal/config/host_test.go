package config

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const hostYAML = `stdio:
  scopes: [ops:read]
providers:
  host:
    enabled: true
    poll_interval: 45s
    systemd:
      units: ["app-*.service", nginx.service]
      restart: ["app-*.service"]
    docker:
      endpoint: unix:///run/docker-proxy.sock
      containers: [web, "compose:shop/*"]
      restart: ["compose:shop/*"]
`

func TestHostConfigRoundTrip(t *testing.T) {
	c, err := Parse([]byte(hostYAML), "host")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := c.Effective()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(effective, "effective")
	if err != nil {
		t.Fatalf("%v\n%s", err, effective)
	}
	if !reflect.DeepEqual(c.Providers.Host, got.Providers.Host) || !strings.Contains(string(effective), "poll_interval: 45s") {
		t.Fatalf("host changed: %+v / %+v\n%s", c.Providers.Host, got.Providers.Host, effective)
	}
	again, err := got.Effective()
	if err != nil || !bytes.Equal(effective, again) {
		t.Fatalf("effective config changed: %v", err)
	}
	if d := Default(); d.Providers.Host.Enabled {
		t.Fatal("host provider enabled by default")
	}
}

func TestHostConfigRejects(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"kubernetes mode": {"stdio:\n  scopes: [ops:read]\n", "mode: kubernetes\n", "cannot enable providers.host"},
		"remote docker":   {"unix:///run/docker-proxy.sock", "http://10.0.0.5:2375", "docker.endpoint"},
		"tcp docker":      {"unix:///run/docker-proxy.sock", "tcp://127.0.0.1:2375", "docker.endpoint"},
		"relative socket": {"unix:///run/docker-proxy.sock", "unix://run/docker.sock", "docker.endpoint"},
		"slow poll":       {"poll_interval: 45s", "poll_interval: 1h", "poll_interval"},
		"fast poll":       {"poll_interval: 45s", "poll_interval: 1s", "poll_interval"},
		"option pattern":  {`nginx.service]`, `"--help"]`, "invalid pattern"},
		"bad compose":     {`"compose:shop/*"]`, `"compose:shop"]`, "compose:project/service"},
		"unit with slash": {`nginx.service]`, `"a/b.service"]`, "invalid pattern"},
	} {
		in := strings.Replace(hostYAML, tc.from, tc.to, 1)
		if name == "kubernetes mode" {
			in = "mode: kubernetes\n" + strings.Replace(hostYAML, tc.from, "", 1)
		}
		if _, err := Parse([]byte(in), name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	ok := strings.Replace(hostYAML, "unix:///run/docker-proxy.sock", "http://127.0.0.1:2375", 1)
	if _, err := Parse([]byte(ok), "loopback proxy"); err != nil {
		t.Fatalf("loopback proxy: %v", err)
	}
}
