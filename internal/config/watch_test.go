package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchLoad(t *testing.T) {
	const kube = "providers:\n  kubernetes:\n    enabled: true\n    clusters: [{name: main}, {name: prod}]\n"
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"https", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/webhook/opsgate?key=s\n", ""},
		{"loopback http", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: http://127.0.0.1:5678/webhook/opsgate\n", ""},
		{"remote http", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: http://n8n.example/hook\n", "watch.outputs"},
		{"private http refused", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: http://10.43.0.10:5678/hook\n", "watch.outputs"},
		{"private http", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      allow_private_http: true\n      url: http://10.43.0.10:5678/hook\n", ""},
		{"private name", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      allow_private_http: true\n      url: http://n8n.n8n.svc.cluster.local:5678/hook\n", ""},
		{"name refused", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: http://n8n.n8n.svc:5678/hook\n", "watch.outputs"},
		{"public ip refused", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      allow_private_http: true\n      url: http://8.8.8.8/hook\n", "watch.outputs"},
		{"credentials", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://u:p@n8n.example/hook\n", "watch.outputs"},
		{"approval origin", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: https://ops.example\n", ""},
		{"approval origin slash", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: https://ops.example/\n", ""},
		{"approval http", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: http://ops.example\n", "approval_url"},
		{"approval path", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: https://ops.example/x\n", "approval_url"},
		{"approval credentials", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: https://u:p@ops.example\n", "approval_url"},
		{"approval query", "watch:\n  outputs:\n    - name: hook\n      type: webhook\n      url: https://n8n.example/h\n  approval_url: https://ops.example?x=1\n", "approval_url"},
		{"batch", "watch:\n  batch: 100ms\n", "watch.batch"},
		{"cooldown", "watch:\n  cooldown: 48h\n", "watch.cooldown"},
		{"max alerts", "watch:\n  max_alerts: 0\n", "watch.max_alerts"},
		{"unknown cluster", "watch:\n  clusters: [staging]\n", "unknown cluster"},
		{"strict", "watch:\n  webhok_url: https://x\n", "field webhok_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(kube+tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("Load() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateWatch(t *testing.T) {
	cfg := Default()
	if err := cfg.ValidateWatch(); err == nil || !strings.Contains(err.Error(), "kubernetes") {
		t.Fatal(err)
	}
	cfg.Providers.Kubernetes.Enabled = true
	if err := cfg.ValidateWatch(); err == nil || !strings.Contains(err.Error(), "watch.outputs") {
		t.Fatal(err)
	}
	cfg.Watch.Outputs = []WatchOutput{{Name: "hook", Type: "webhook", URL: "https://n8n.example/hook"}}
	if err := cfg.ValidateWatch(); err != nil {
		t.Fatal(err)
	}
	cfg.Providers.Kubernetes.Clusters = []KubernetesCluster{{Name: "main"}, {Name: "prod"}}
	cfg.Watch.Clusters = []string{"prod"}
	if got := cfg.WatchClusters(); len(got) != 1 || got[0].Name != "prod" {
		t.Fatal(got)
	}
}

func TestWatchRemovedKeys(t *testing.T) {
	for _, key := range []string{"webhook_url", "secret_file", "allow_private_http"} {
		for _, value := range []string{"null", "false", `""`, `"legacy-secret"`} {
			t.Run(key+"/"+value, func(t *testing.T) {
				_, err := Parse([]byte("unknown_field: true\nwatch:\n  "+key+": "+value+"\n"), "test")
				want := "watch." + key + " was removed; move it to watch.outputs as {name: ..., type: webhook, url: ..., secret_file: ..., allow_private_http: ...}"
				if err == nil || err.Error() != want {
					t.Fatalf("Parse() = %v, want %q", err, want)
				}
			})
		}
	}
}
