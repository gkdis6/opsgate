package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (p *provider) planRollout(ctx context.Context, _ tool.Caller, in rolloutInput) (tool.Plan, error) {
	if len(p.cfg.RolloutRestart) == 0 {
		return tool.Plan{}, tool.Denied("k8s_rollout_restart is disabled on cluster %s", p.cfg.Name)
	}
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
	metadata, err := meta.Accessor(obj)
	if err != nil {
		return tool.Plan{}, err
	}
	templateMeta, _ := template["metadata"].(map[string]any)
	annotations, _ := templateMeta["annotations"].(map[string]any)
	summary := fmt.Sprintf("Rollout restart %s %s/%s (UID %q; replicas %d, ready %d, images %v)", in.Kind, in.Namespace, in.Name, metadata.GetUID(), desiredCount, ready, images)
	return identityPlan(metadata.GetUID(), metadata.GetResourceVersion(), summary, annotations != nil)
}

func (p *provider) applyRollout(ctx context.Context, _ tool.Caller, in rolloutInput, plan tool.Plan) (string, error) {
	if len(p.cfg.RolloutRestart) == 0 {
		return "", tool.Denied("k8s_rollout_restart is disabled on cluster %s", p.cfg.Name)
	}
	if _, err := writeKind(in.Kind, true); err != nil {
		return "", err
	}
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.RolloutRestart); err != nil {
		return "", err
	}
	identity, err := plannedIdentity(plan)
	if err != nil {
		return "", err
	}
	ops := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": identity.uid}}
	if !identity.hasAnnotations {
		// Avoid overwriting annotations added by another writer since planning.
		ops = append(ops, map[string]any{"op": "test", "path": "/metadata/resourceVersion", "value": identity.version},
			map[string]any{"op": "add", "path": "/spec/template/metadata/annotations", "value": map[string]string{}})
	}
	ops = append(ops, map[string]any{"op": "add", "path": "/spec/template/metadata/annotations/kubectl.kubernetes.io~1restartedAt", "value": time.Now().UTC().Format(time.RFC3339)})
	body, err := json.Marshal(ops)
	if err != nil {
		return "", err
	}
	switch in.Kind {
	case "deployment":
		_, err = p.client.AppsV1().Deployments(in.Namespace).Patch(ctx, in.Name, types.JSONPatchType, body, metav1.PatchOptions{})
	case "statefulset":
		_, err = p.client.AppsV1().StatefulSets(in.Namespace).Patch(ctx, in.Name, types.JSONPatchType, body, metav1.PatchOptions{})
	case "daemonset":
		_, err = p.client.AppsV1().DaemonSets(in.Namespace).Patch(ctx, in.Name, types.JSONPatchType, body, metav1.PatchOptions{})
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Requested rollout restart of %s %s/%s", in.Kind, in.Namespace, in.Name), nil
}
