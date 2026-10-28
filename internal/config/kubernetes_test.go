package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubernetesLoad(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
		count            int
	}{
		{"old single", "kubeconfig: /tmp/kube\n    namespaces: [apps]", "", 1},
		{"named single", "name: local", "", 1},
		{"multiple", "clusters:\n      - name: kind\n      - name: prod\n        protected: true", "", 2},
		{"empty", "clusters: []", "must not be empty", 0},
		{"duplicate", "clusters: [{name: kind}, {name: kind}]", "unique", 0},
		{"missing name", "clusters: [{}]", "names", 0},
		{"invalid name", "clusters: [{name: Prod}]", "names", 0},
		{"single conflict", "context: chosen\n    clusters: [{name: kind}]", "mutually exclusive", 0},
		{"inline strict", "namespces: [apps]", "field namespces", 0},
		{"list strict", "clusters: [{name: kind, namespces: [apps]}]", "field namespces", 0},
		{"negative replicas", "clusters: [{name: kind, max_replicas: -1}]", "must not be negative", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte("providers:\n  kubernetes:\n    enabled: true\n    "+tc.yaml+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Load() = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			clusters := cfg.Providers.Kubernetes.ClusterList()
			if len(clusters) != tc.count {
				t.Fatal(clusters)
			}
			for _, c := range clusters {
				if c.MaxLogLines != 500 || c.MaxReplicas != 10 {
					t.Fatal(c)
				}
			}
			if tc.name == "old single" && (clusters[0].Name != "default" || clusters[0].Kubeconfig != "/tmp/kube" || clusters[0].Namespaces[0] != "apps") {
				t.Fatal(clusters)
			}
			if tc.name == "named single" && clusters[0].Name != "local" {
				t.Fatal(clusters)
			}
			if tc.name == "multiple" && (clusters[0].Name != "kind" || clusters[1].Name != "prod" || !clusters[1].Protected) {
				t.Fatal(clusters)
			}
		})
	}
}

func TestKubernetesValidation(t *testing.T) {
	for _, name := range []string{"a", "prod-b", strings.Repeat("a", 63), "", "A", "-prod", "prod-", "a_b", strings.Repeat("a", 64)} {
		t.Run("name "+name, func(t *testing.T) {
			c := Default()
			c.Providers.Kubernetes.Enabled = true
			c.Providers.Kubernetes.Clusters = []KubernetesCluster{{Name: name}}
			valid := name == "a" || name == "prod-b" || name == strings.Repeat("a", 63)
			if err := c.Validate(); (err == nil) != valid {
				t.Fatal(err)
			}
		})
	}
	for _, single := range []KubernetesCluster{
		{Name: "single"}, {Protected: true}, {Kubeconfig: "config"}, {Context: "ctx"},
		{Namespaces: []string{"apps"}}, {DenyNamespaces: []string{"private"}}, {AllowLogs: true},
		{ShowConfigMapData: true}, {MaxLogLines: 1}, {MaxReplicas: 1},
		{RolloutRestart: []string{"apps/web"}}, {Scale: []string{"apps/web"}}, {DeletePod: []string{"apps/web"}},
	} {
		c := Default()
		c.Providers.Kubernetes = Kubernetes{Enabled: true, KubernetesCluster: single, Clusters: []KubernetesCluster{{Name: "kind"}}}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("%+v: %v", single, err)
		}
		c.Providers.Kubernetes.Enabled = false
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClusterListDefaults(t *testing.T) {
	for _, k := range []Kubernetes{
		Default().Providers.Kubernetes,
		{Clusters: []KubernetesCluster{{Name: "kind"}, {Name: "prod", MaxLogLines: 50, MaxReplicas: 3}}},
	} {
		clusters := k.ClusterList()
		if clusters[0].Name == "" || clusters[0].MaxLogLines != 500 || clusters[0].MaxReplicas != 10 {
			t.Fatal(clusters)
		}
		if len(clusters) == 2 && (clusters[1].MaxLogLines != 50 || clusters[1].MaxReplicas != 3 || k.Clusters[0].MaxReplicas != 0) {
			t.Fatal(clusters, k)
		}
	}
}

func TestSingleClusterKeepsExplicitZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opsgate.yaml")
	if err := os.WriteFile(path, []byte("providers:\n  kubernetes:\n    max_replicas: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.Providers.Kubernetes.ClusterList(); c[0].MaxReplicas != 0 {
		t.Fatal(c)
	}
}

func TestExampleConfig(t *testing.T) {
	if _, err := Load("../../opsgate.example.yaml"); err != nil {
		t.Fatal(err)
	}
}
