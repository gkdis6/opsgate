package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (p *provider) planRollout(ctx context.Context, _ tool.Caller, in rolloutInput) (tool.Plan, error) {
	r, err := writeKind(in.Kind, true)
	if err != nil {
		return tool.Plan{}, err
	}
	if err = p.writeAllowed(in.Namespace, in.Name, p.cfg.RolloutRestart); err != nil {
		return tool.Plan{}, err
	}
	obj, err := p.getResource(ctx, r, in.Namespace, in.Name)
	if err != nil {
		return tool.Plan{}, err
	}
	m, err := objectMap(obj)
	if err != nil {
		return tool.Plan{}, err
	}
	desiredCount, ready := desired(m), num(m, "status", "readyReplicas")
	if in.Kind == "daemonset" {
		desiredCount, ready = num(m, "status", "desiredNumberScheduled"), num(m, "status", "numberReady")
	}
	// Quote untrusted image names so they cannot insert lines in the preview.
	var images []string
	spec, _ := m["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	for _, field := range []string{"initContainers", "containers"} {
		containers, _ := podSpec[field].([]any)
		for _, item := range containers {
			c, _ := item.(map[string]any)
			image, _ := c["image"].(string)
			if len(image) > 1024 || len(images) >= 64 {
				return tool.Plan{}, fmt.Errorf("workload image summary exceeds limit")
			}
			images = append(images, fmt.Sprintf("%q", image))
		}
	}
	return tool.Plan{Summary: redact.String(fmt.Sprintf("Rollout restart %s %s/%s (replicas %d, ready %d, images %v)", in.Kind, in.Namespace, in.Name, desiredCount, ready, images))}, nil
}

func (p *provider) applyRollout(ctx context.Context, _ tool.Caller, in rolloutInput) (string, error) {
	if _, err := writeKind(in.Kind, true); err != nil {
		return "", err
	}
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.RolloutRestart); err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339)}}}}})
	if err != nil {
		return "", err
	}
	switch in.Kind {
	case "deployment":
		_, err = p.client.AppsV1().Deployments(in.Namespace).Patch(ctx, in.Name, types.StrategicMergePatchType, body, metav1.PatchOptions{})
	case "statefulset":
		_, err = p.client.AppsV1().StatefulSets(in.Namespace).Patch(ctx, in.Name, types.StrategicMergePatchType, body, metav1.PatchOptions{})
	case "daemonset":
		_, err = p.client.AppsV1().DaemonSets(in.Namespace).Patch(ctx, in.Name, types.StrategicMergePatchType, body, metav1.PatchOptions{})
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Requested rollout restart of %s %s/%s", in.Kind, in.Namespace, in.Name), nil
}
