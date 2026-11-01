package config

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Watch configures `opsgate watch`, a separate long-running process that
// watches Kubernetes and POSTs alerts to a webhook. The MCP server does not
// use it.
type Watch struct {
	// WebhookURL receives alert batches as JSON POSTs, e.g. an n8n webhook
	// that forwards them to chat. Required by `opsgate watch`.
	WebhookURL string `yaml:"webhook_url"`
	// AllowPrivateHTTP also permits http to receivers without TLS on a
	// private network, such as a Kubernetes Service name or ClusterIP.
	// Every connection must then reach a private or loopback address after
	// DNS resolution, and alerts cross that network unencrypted.
	AllowPrivateHTTP bool `yaml:"allow_private_http"`
	// SecretFile optionally holds a key used to sign each POST with
	// HMAC-SHA256 (X-Opsgate-Signature).
	SecretFile string `yaml:"secret_file"`
	// Clusters limits watching to these configured cluster names; empty
	// watches every configured cluster.
	Clusters []string `yaml:"clusters"`
	// ProblemsOnly drops informational changes (images, replicas,
	// ConfigMap keys, objects added or removed) and sends warnings only.
	ProblemsOnly bool `yaml:"problems_only"`
	// Batch is how long alerts are gathered into one POST.
	Batch time.Duration `yaml:"batch"`
	// Cooldown suppresses repeats of the same warning for one object.
	Cooldown time.Duration `yaml:"cooldown"`
	// MaxAlerts caps the alerts in one POST; the rest are counted as dropped.
	MaxAlerts int `yaml:"max_alerts"`
}

func (w Watch) validate(k Kubernetes) error {
	if w.WebhookURL != "" {
		u, err := url.Parse(w.WebhookURL)
		if err != nil || len(w.WebhookURL) > 4096 || u.Hostname() == "" || u.User != nil || u.Opaque != "" || strings.Contains(w.WebhookURL, "#") ||
			(u.Scheme != "https" && !(u.Scheme == "http" && (isLoopback(strings.ToLower(u.Hostname())) || w.AllowPrivateHTTP && privateHost(u.Hostname())))) {
			return fmt.Errorf("watch.webhook_url must use https or loopback http (or private http with allow_private_http) without credentials or fragments")
		}
	}
	if w.Batch < time.Second || w.Batch > 10*time.Minute {
		return fmt.Errorf("watch.batch must be between 1s and 10m")
	}
	if w.Cooldown < 0 || w.Cooldown > 24*time.Hour {
		return fmt.Errorf("watch.cooldown must be between 0 and 24h")
	}
	if w.MaxAlerts < 1 || w.MaxAlerts > 500 {
		return fmt.Errorf("watch.max_alerts must be between 1 and 500")
	}
	var names []string
	for _, c := range k.ClusterList() {
		names = append(names, c.Name)
	}
	for _, name := range w.Clusters {
		if !slices.Contains(names, name) {
			return fmt.Errorf("watch.clusters: unknown cluster %q", name)
		}
	}
	return nil
}

// privateHost accepts names, whose addresses are checked when connecting,
// and IP literals in private ranges.
func privateHost(host string) bool {
	ip := net.ParseIP(host)
	return ip == nil || ip.IsPrivate()
}

// ValidateWatch checks the settings required by `opsgate watch`.
func (c Config) ValidateWatch() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !c.Providers.Kubernetes.Enabled {
		return fmt.Errorf("opsgate watch requires providers.kubernetes.enabled")
	}
	if c.Watch.WebhookURL == "" {
		return fmt.Errorf("opsgate watch requires watch.webhook_url")
	}
	return nil
}

// WatchClusters returns the clusters `opsgate watch` follows.
func (c Config) WatchClusters() []KubernetesCluster {
	all := c.Providers.Kubernetes.ClusterList()
	if len(c.Watch.Clusters) == 0 {
		return all
	}
	var result []KubernetesCluster
	for _, cluster := range all {
		if slices.Contains(c.Watch.Clusters, cluster.Name) {
			result = append(result, cluster)
		}
	}
	return result
}
