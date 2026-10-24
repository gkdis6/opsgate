package kube

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type logsInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
	Tail      int    `json:"tail,omitempty"`
	Since     string `json:"since,omitempty"`
	Previous  bool   `json:"previous,omitempty"`
	Grep      string `json:"grep,omitempty"`
}

func (p *provider) logs(ctx context.Context, _ tool.Caller, in logsInput) (string, error) {
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
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
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
