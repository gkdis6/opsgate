package config

import (
	"fmt"
	"reflect"
	"regexp"
)

// Kubernetes supports either a single cluster or an ordered list of clusters.
type Kubernetes struct {
	Enabled bool `yaml:"enabled"`
	// Single-cluster form, kept for existing configurations.
	KubernetesCluster `yaml:",inline"`
	// Clusters is mutually exclusive with the single-cluster fields.
	Clusters []KubernetesCluster `yaml:"clusters,omitempty"`
}

// KubernetesCluster holds connection and policy settings for one cluster.
type KubernetesCluster struct {
	// Name is required in Clusters; the single-cluster form defaults to "default".
	Name string `yaml:"name"`
	// Protected requires fresh approval for every write.
	Protected bool `yaml:"protected"`
	// Kubeconfig path. Empty uses in-cluster config, then $KUBECONFIG,
	// then ~/.kube/config.
	Kubeconfig string `yaml:"kubeconfig"`
	Context    string `yaml:"context"`
	// Namespaces is a glob allowlist. Empty allows none; "*" allows all.
	Namespaces []string `yaml:"namespaces"`
	// DenyNamespaces wins over Namespaces.
	DenyNamespaces []string `yaml:"deny_namespaces"`
	// AllowLogs enables k8s_logs.
	AllowLogs bool `yaml:"allow_logs"`
	// ShowConfigMapData returns ConfigMap values; otherwise only keys.
	ShowConfigMapData bool `yaml:"show_configmap_data"`
	MaxLogLines       int  `yaml:"max_log_lines"`
	// Write actions. Each list holds "namespace/name" globs; empty
	// disables the tool. Namespace allow/deny rules still apply.
	RolloutRestart []string `yaml:"rollout_restart"`
	Scale          []string `yaml:"scale"`
	DeletePod      []string `yaml:"delete_pod"`
	// MaxReplicas caps k8s_scale.
	MaxReplicas int32 `yaml:"max_replicas"`
}

// ClusterList returns the effective clusters. The single-cluster form gets
// its numeric defaults from Default(), so an explicit zero keeps its meaning
// there; list entries have no such defaults and treat zero as unset.
func (k Kubernetes) ClusterList() []KubernetesCluster {
	if k.Clusters == nil {
		c := k.KubernetesCluster
		if c.Name == "" {
			c.Name = "default"
		}
		return []KubernetesCluster{c}
	}
	clusters := append([]KubernetesCluster(nil), k.Clusters...)
	for i := range clusters {
		if clusters[i].MaxLogLines == 0 {
			clusters[i].MaxLogLines = 500
		}
		if clusters[i].MaxReplicas == 0 {
			clusters[i].MaxReplicas = 10
		}
	}
	return clusters
}

func (k Kubernetes) validate() error {
	if !k.Enabled {
		return nil
	}
	if k.Clusters != nil {
		single := k.KubernetesCluster
		if single.MaxLogLines == 500 {
			single.MaxLogLines = 0
		}
		if single.MaxReplicas == 10 {
			single.MaxReplicas = 0
		}
		if !reflect.ValueOf(single).IsZero() {
			return fmt.Errorf("providers.kubernetes: clusters and single-cluster fields are mutually exclusive")
		}
		if len(k.Clusters) == 0 {
			return fmt.Errorf("providers.kubernetes.clusters must not be empty")
		}
	}
	name := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	seen := map[string]bool{}
	for _, c := range k.ClusterList() {
		if !name.MatchString(c.Name) || seen[c.Name] {
			return fmt.Errorf("providers.kubernetes: cluster names must be unique lowercase DNS labels of at most 63 characters: %q", c.Name)
		}
		seen[c.Name] = true
		if c.MaxReplicas < 0 {
			return fmt.Errorf("providers.kubernetes cluster %q: max_replicas must not be negative", c.Name)
		}
	}
	return nil
}
