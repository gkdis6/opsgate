package action

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/gkdis6/opsgate/internal/hosttarget"
	"github.com/gkdis6/opsgate/internal/redact"
)

// Choice retains the image restoration fields for existing tool consumers.
type Choice struct {
	Tool             string `json:"tool"`
	Label            string `json:"label"`
	LabelKO          string `json:"label_ko"`
	URL              string `json:"url"`
	Reason           string `json:"reason"`
	ReasonKO         string `json:"reason_ko"`
	Source           string `json:"source"`
	Container        string `json:"container,omitempty"`
	CurrentImage     string `json:"current_image,omitempty"`
	Image            string `json:"image,omitempty"`
	SourceReplicaSet string `json:"source_replicaset,omitempty"`
}

type Options struct {
	Actions []Choice `json:"actions"`
	Hint    string   `json:"hint,omitempty"`
	PodGone bool     `json:"pod_gone,omitempty"`
}

var optionSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// OptionsURL builds a secret-free navigation link, never an approval grant.
func OptionsURL(base, cluster, namespace, pod, workload string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (base != "" && (u.Host == "" || u.Scheme != "https")) {
		return "", fmt.Errorf("approval base must be an HTTPS origin")
	}
	for _, s := range []string{cluster, namespace, pod} {
		if len(s) > 253 || !optionSegment.MatchString(s) || redact.String(s) != s || strings.Contains(s, redact.Mask) {
			return "", fmt.Errorf("invalid options target")
		}
	}
	if workload != "" {
		kind, name, ok := strings.Cut(workload, "/")
		if !ok || (kind != "deployment" && kind != "statefulset" && kind != "daemonset") || len(name) > 253 || !optionSegment.MatchString(name) || redact.String(name) != name {
			return "", fmt.Errorf("invalid workload hint")
		}
		u.RawQuery = url.Values{"workload": {workload}}.Encode()
	}
	u.Path = "/approve/options/" + cluster + "/" + namespace + "/" + pod
	return u.String(), nil
}

// HostOptionsURL links to live choices for a host unit or container. It is
// navigation only; every action behind it still needs its own approval.
func HostOptionsURL(base, kind, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u == nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (base != "" && (u.Host == "" || u.Scheme != "https")) {
		return "", fmt.Errorf("approval base must be an HTTPS origin")
	}
	if !hosttarget.Valid(kind, name) {
		return "", fmt.Errorf("invalid host options target")
	}
	u.Path = "/approve/host-options/" + kind + "/" + name
	return u.String(), nil
}
