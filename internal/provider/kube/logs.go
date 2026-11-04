package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type logsInput struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace"`
	Pod       string `json:"pod" jsonschema:"Required pod name"`
	Container string `json:"container,omitempty" jsonschema:"Container name; required unless the pod has exactly one container"`
	Tail      int    `json:"tail,omitempty" jsonschema:"Maximum log lines; default 200; clamped to 1 through configured MaxLogLines (fallback 500)"`
	Since     string `json:"since,omitempty" jsonschema:"Positive Go duration up to 168h; omitted means no time filter"`
	Previous  bool   `json:"previous,omitempty" jsonschema:"Read previous container instance; default false"`
	Grep      string `json:"grep,omitempty" jsonschema:"Case-insensitive literal substring filter; default no filter"`
}

func (p *provider) logs(ctx context.Context, _ tool.Caller, in logsInput) (string, error) {
	if !p.cfg.AllowLogs {
		return "", tool.Denied("k8s_logs is disabled on cluster %s", p.cfg.Name)
	}
	if err := p.checkNamespace(in.Namespace); err != nil {
		return "", err
	}
	if err := validName(in.Pod, false); err != nil {
		return "", err
	}
	if in.Container != "" {
		if err := validName(in.Container, false); err != nil {
			return "", err
		}
	}
	var seconds *int64
	if in.Since != "" {
		d, err := duration(in.Since, "")
		if err != nil {
			return "", err
		}
		n := int64((d + time.Second - 1) / time.Second)
		seconds = &n
	}
	pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Pod, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	var names []string
	for _, c := range pod.Spec.Containers {
		names = append(names, c.Name)
	}
	for _, c := range pod.Spec.InitContainers {
		names = append(names, c.Name)
	}
	for _, c := range pod.Spec.EphemeralContainers {
		names = append(names, c.Name)
	}
	if in.Container == "" {
		if len(names) != 1 {
			return "", fmt.Errorf("container is required; available containers: %s", strings.Join(names, ", "))
		}
		in.Container = names[0]
	} else {
		found := false
		for _, n := range names {
			if n == in.Container {
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("unknown container; available containers: %s", strings.Join(names, ", "))
		}
	}
	max := p.cfg.MaxLogLines
	if max < 1 {
		max = 500
	}
	tail := int64(clamp(in.Tail, 200, max))
	bytes := int64(1 << 20)
	stream, err := p.client.CoreV1().Pods(in.Namespace).GetLogs(in.Pod, &corev1.PodLogOptions{Container: in.Container, TailLines: &tail, SinceSeconds: seconds, Previous: in.Previous, LimitBytes: &bytes}).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, bytes))
	if err != nil {
		return "", err
	}
	text := string(data)
	text = redact.Fragment(text, redact.Ranges(text), 0, len(text))
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > int(tail) {
		lines = lines[len(lines)-int(tail):]
	}
	var out []string
	needle := strings.ToLower(in.Grep)
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), needle) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), nil
}
