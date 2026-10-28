package kube

import (
	"context"
	"fmt"

	"github.com/gkdis6/opsgate/internal/tool"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (p *provider) checkScale(in scaleInput) error {
	if len(p.cfg.Scale) == 0 {
		return tool.Denied("k8s_scale is disabled on cluster %s", p.cfg.Name)
	}
	if _, err := writeKind(in.Kind, false); err != nil {
		return err
	}
	if err := p.writeAllowed(in.Namespace, in.Name, p.cfg.Scale); err != nil {
		return err
	}
	if in.Replicas < 0 || in.Replicas > p.cfg.MaxReplicas {
		return fmt.Errorf("replicas must be between 0 and %d", p.cfg.MaxReplicas)
	}
	return nil
}

func (p *provider) getScale(ctx context.Context, in scaleInput) (*autoscalingv1.Scale, error) {
	if in.Kind == "deployment" {
		return p.client.AppsV1().Deployments(in.Namespace).GetScale(ctx, in.Name, metav1.GetOptions{})
	}
	return p.client.AppsV1().StatefulSets(in.Namespace).GetScale(ctx, in.Name, metav1.GetOptions{})
}

func (p *provider) planScale(ctx context.Context, _ tool.Caller, in scaleInput) (tool.Plan, error) {
	if err := p.checkScale(in); err != nil {
		return tool.Plan{}, err
	}
	scale, err := p.getScale(ctx, in)
	if err != nil {
		return tool.Plan{}, err
	}
	summary := fmt.Sprintf("Scale %s %s/%s: %d → %d replicas (UID %q, resourceVersion %q)", in.Kind, in.Namespace, in.Name, scale.Spec.Replicas, in.Replicas, scale.UID, scale.ResourceVersion)
	return identityPlan(scale.UID, scale.ResourceVersion, summary, false)
}

func (p *provider) applyScale(ctx context.Context, _ tool.Caller, in scaleInput, plan tool.Plan) (string, error) {
	if err := p.checkScale(in); err != nil {
		return "", err
	}
	snapshot, err := plannedIdentity(plan)
	if err != nil {
		return "", err
	}
	scale, err := p.getScale(ctx, in)
	if err != nil {
		return "", err
	}
	if scale.UID != snapshot.uid || scale.ResourceVersion != snapshot.version {
		return "", tool.Denied("resource changed since preview")
	}
	scale = scale.DeepCopy()
	scale.Spec.Replicas = in.Replicas
	scale.ResourceVersion = snapshot.version
	if in.Kind == "deployment" {
		_, err = p.client.AppsV1().Deployments(in.Namespace).UpdateScale(ctx, in.Name, scale, metav1.UpdateOptions{})
	} else {
		_, err = p.client.AppsV1().StatefulSets(in.Namespace).UpdateScale(ctx, in.Name, scale, metav1.UpdateOptions{})
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Scaled %s %s/%s to %d replicas", in.Kind, in.Namespace, in.Name, in.Replicas), nil
}
