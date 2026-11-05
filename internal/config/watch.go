package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Watch configures `opsgate watch`, a separate long-running process that
// watches Kubernetes and delivers alerts. The MCP server does not
// use it.
type Watch struct {
	Outputs []WatchOutput `yaml:"outputs,omitempty"`
	// WebhookURL receives alert batches as JSON POSTs, e.g. an n8n webhook
	// that forwards them to chat. Outputs and Email can also deliver alerts.
	WebhookURL string `yaml:"webhook_url"`
	// Email delivers alerts by SMTP, alongside or instead of the webhook.
	Email WatchEmail `yaml:"email,omitempty"`
	// AllowPrivateHTTP also permits http to receivers without TLS on a
	// private network, such as a Kubernetes Service name or ClusterIP.
	// Every connection must then reach a private or loopback address after
	// DNS resolution, and alerts cross that network unencrypted.
	AllowPrivateHTTP bool `yaml:"allow_private_http"`
	// ApprovalURL is the public https origin of the opsgate server. When set,
	// restart suggestions link to its approval page. The link holds no secret:
	// opening it still needs sign-in and approval.
	ApprovalURL string `yaml:"approval_url"`
	// SecretFile optionally holds a key used to sign each POST with
	// HMAC-SHA256 (X-Opsgate-Signature).
	SecretFile string `yaml:"secret_file"`
	// Clusters limits watching to these configured cluster names; empty
	// watches every configured cluster.
	Clusters []string `yaml:"clusters"`
	// ProblemsOnly drops informational changes (images, replicas,
	// ConfigMap keys, objects added or removed) and sends warnings only.
	ProblemsOnly bool `yaml:"problems_only"`
	// Batch is how long alerts are gathered into one webhook POST.
	Batch time.Duration `yaml:"batch"`
	// Cooldown suppresses repeats of the same warning for one object.
	Cooldown time.Duration `yaml:"cooldown"`
	// MaxAlerts caps each batch; the rest are counted as dropped.
	MaxAlerts int `yaml:"max_alerts"`
}

// WatchEmail configures SMTP delivery, enabled by Host.
type WatchEmail struct {
	Host string `yaml:"host"`
	// Port 465 uses implicit TLS; every other port requires STARTTLS.
	Port int `yaml:"port"`
	// Username and PasswordFile are set together, or not at all for a relay
	// that needs no authentication.
	Username     string   `yaml:"username"`
	PasswordFile string   `yaml:"password_file"`
	From         string   `yaml:"from"`
	To           []string `yaml:"to"`
	// Batch is how long alerts are gathered into one message.
	Batch time.Duration `yaml:"batch"`
	// ProblemsOnly sends warnings only by email.
	ProblemsOnly bool `yaml:"problems_only"`
}

// IsZero omits disabled email from effective configuration.
func (e WatchEmail) IsZero() bool { return e.Host == "" }

func (e WatchEmail) validate() error {
	// Parse rejects fields set without a host.
	if e.Host == "" {
		return nil
	}
	if strings.ContainsAny(e.Host, ":/\\") || strings.ContainsFunc(e.Host, unicode.IsSpace) {
		return fmt.Errorf("watch.email.host must be a host without whitespace, scheme, port or slash")
	}
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("watch.email.port must be between 1 and 65535")
	}
	if (e.Username == "") != (e.PasswordFile == "") {
		return fmt.Errorf("watch.email.username and watch.email.password_file must be set together")
	}
	if !bareAddress(e.From) {
		return fmt.Errorf("watch.email.from must be a bare email address")
	}
	if len(e.To) < 1 || len(e.To) > 20 {
		return fmt.Errorf("watch.email.to must have 1 to 20 addresses")
	}
	for _, to := range e.To {
		if !bareAddress(to) {
			return fmt.Errorf("watch.email.to must contain bare email addresses")
		}
	}
	if e.Batch < time.Minute || e.Batch > time.Hour {
		return fmt.Errorf("watch.email.batch must be between 1m and 1h")
	}
	return nil
}

func bareAddress(value string) bool {
	a, err := mail.ParseAddress(value)
	return err == nil && a.Name == "" && a.Address == value && !strings.ContainsAny(value, "\r\n")
}

func (w Watch) validate(k Kubernetes) error {
	if err := w.Email.validate(); err != nil {
		return err
	}
	if len(w.Outputs) > 20 {
		return fmt.Errorf("watch.outputs supports at most 20 outputs")
	}
	outputs := map[string]bool{}
	for i, o := range w.Outputs {
		if err := o.validate(); err != nil {
			return fmt.Errorf("watch.outputs[%d]: %w", i, err)
		}
		if outputs[o.Name] {
			return fmt.Errorf("watch.outputs: duplicate name %q", o.Name)
		}
		outputs[o.Name] = true
	}
	if w.WebhookURL != "" && !validWatchURL(w.WebhookURL, w.AllowPrivateHTTP) {
		return fmt.Errorf("watch.webhook_url must use https or loopback http (or private http with allow_private_http) without credentials or fragments")
	}

	if w.ApprovalURL != "" {
		u, err := url.Parse(w.ApprovalURL)
		if err != nil || len(w.ApprovalURL) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
			strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || strings.Contains(w.ApprovalURL, "#") {
			return fmt.Errorf("watch.approval_url must be an https origin without credentials, path, query or fragment")
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
	if c.Watch.WebhookURL == "" && c.Watch.Email.Host == "" && len(c.Watch.Outputs) == 0 {
		return fmt.Errorf("opsgate watch requires watch.webhook_url, watch.outputs or watch.email.host")
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

// ValidateURL checks a URL loaded from a file without exposing its contents.
func (o WatchOutput) ValidateURL(value string) error {
	if !validWatchURL(value, o.AllowPrivateHTTP) {
		return fmt.Errorf("url must use https or permitted private/loopback http without credentials or fragments")
	}
	return nil
}

func validWatchURL(value string, private bool) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 4096 && u.Hostname() != "" && u.User == nil && u.Opaque == "" && !strings.Contains(value, "#") &&
		(u.Scheme == "https" || u.Scheme == "http" && (isLoopback(strings.ToLower(u.Hostname())) || private && privateHost(u.Hostname())))
}
