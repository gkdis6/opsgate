package kube

import (
	"context"
	"fmt"

	"github.com/gkdis6/opsgate/internal/tool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (p *provider) planDeletePod(ctx context.Context, _ tool.Caller, in deletePodInput) (tool.Plan, error) {
	if len(p.cfg.DeletePod) == 0 {
		return tool.Plan{}, tool.Denied("k8s_delete_pod is disabled on cluster %s", p.cfg.Name)
	}
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.DeletePod); err != nil {
		return tool.Plan{}, err
	}
	pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return tool.Plan{}, err
	}
	owner := metav1.GetControllerOf(pod)
	effect := "no controller owner; will NOT be recreated"
	if owner != nil {
		effect = fmt.Sprintf("controller owner %q/%q", owner.Kind, owner.Name)
	}
	summary := fmt.Sprintf("Delete pod %s/%s (UID %q; %s)", in.Namespace, in.Name, pod.UID, effect)
	return identityPlan(pod.UID, pod.ResourceVersion, summary, false)
}

func (p *provider) applyDeletePod(ctx context.Context, _ tool.Caller, in deletePodInput, plan tool.Plan) (string, error) {
	if len(p.cfg.DeletePod) == 0 {
		return "", tool.Denied("k8s_delete_pod is disabled on cluster %s", p.cfg.Name)
	}
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.DeletePod); err != nil {
		return "", err
	}
	snapshot, err := plannedIdentity(plan)
	if err != nil {
		return "", err
	}
	pod, err := p.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if pod.UID != snapshot.uid {
		return "", tool.Denied("pod changed since preview")
	}
	err = p.client.CoreV1().Pods(in.Namespace).Delete(ctx, in.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &snapshot.uid}})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted pod %s/%s (UID %s)", in.Namespace, in.Name, snapshot.uid), nil
}
