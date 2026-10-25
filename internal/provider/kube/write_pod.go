package kube

import (
	"context"
	"fmt"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func podKey(c tool.Caller, in deletePodInput) writeKey {
	return writeKey{user: c.User, transport: c.Transport, action: "delete-pod", namespace: in.Namespace, name: in.Name}
}

func (p *provider) planDeletePod(ctx context.Context, c tool.Caller, in deletePodInput) (tool.Plan, error) {
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
	return p.writes.remember(podKey(c, in), pod.UID, pod.ResourceVersion, redact.String(summary))
}

func (p *provider) applyDeletePod(ctx context.Context, c tool.Caller, in deletePodInput) (string, error) {
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.DeletePod); err != nil {
		return "", err
	}
	snapshot, err := p.writes.take(podKey(c, in))
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
