package kube

import (
	"context"
	"fmt"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// workloadCheck reports whether a workload finished rolling out with every
// replica available, the same condition `kubectl rollout status` waits for.
func (p *provider) workloadCheck(kind, ns, name string) tool.Check {
	return func(ctx context.Context) (tool.Health, error) {
		switch kind {
		case "deployment":
			d, err := p.client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return tool.Health{}, err
			}
			return p.deploymentHealth(ctx, d), nil
		case "statefulset":
			s, err := p.client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return tool.Health{}, err
			}
			return p.statefulSetHealth(ctx, s), nil
		case "daemonset":
			d, err := p.client.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return tool.Health{}, err
			}
			return p.daemonSetHealth(ctx, d), nil
		}
		return tool.Health{Done: true, Detail: "unsupported workload kind"}, nil
	}
}

func (p *provider) deploymentHealth(ctx context.Context, d *appsv1.Deployment) tool.Health {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	if d.Generation > d.Status.ObservedGeneration {
		return tool.Health{Detail: "waiting for the controller"}
	}
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return tool.Health{Done: true, Detail: p.withPodReason(ctx, d.Namespace, d.Spec.Selector, "rollout exceeded its progress deadline")}
		}
	}
	s := d.Status
	switch {
	case s.UpdatedReplicas < want:
		return tool.Health{Detail: p.withPodReason(ctx, d.Namespace, d.Spec.Selector, fmt.Sprintf("updated %d/%d", s.UpdatedReplicas, want))}
	case s.Replicas > s.UpdatedReplicas:
		return tool.Health{Detail: fmt.Sprintf("%d old pods terminating", s.Replicas-s.UpdatedReplicas)}
	case s.AvailableReplicas < s.UpdatedReplicas:
		return tool.Health{Detail: p.withPodReason(ctx, d.Namespace, d.Spec.Selector, fmt.Sprintf("available %d/%d", s.AvailableReplicas, want))}
	}
	return tool.Health{Done: true, Healthy: true, Detail: fmt.Sprintf("available %d/%d", s.AvailableReplicas, want)}
}

func (p *provider) statefulSetHealth(ctx context.Context, st *appsv1.StatefulSet) tool.Health {
	want := int32(1)
	if st.Spec.Replicas != nil {
		want = *st.Spec.Replicas
	}
	if st.Generation > st.Status.ObservedGeneration {
		return tool.Health{Detail: "waiting for the controller"}
	}
	s := st.Status
	if st.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		partitioned := st.Spec.UpdateStrategy.RollingUpdate != nil && st.Spec.UpdateStrategy.RollingUpdate.Partition != nil && *st.Spec.UpdateStrategy.RollingUpdate.Partition > 0
		if !partitioned && (s.UpdatedReplicas < want || s.UpdateRevision != s.CurrentRevision && s.UpdateRevision != "") {
			return tool.Health{Detail: p.withPodReason(ctx, st.Namespace, st.Spec.Selector, fmt.Sprintf("updated %d/%d", s.UpdatedReplicas, want))}
		}
	}
	if s.ReadyReplicas < want || s.AvailableReplicas < want {
		return tool.Health{Detail: p.withPodReason(ctx, st.Namespace, st.Spec.Selector, fmt.Sprintf("available %d/%d", s.AvailableReplicas, want))}
	}
	return tool.Health{Done: true, Healthy: true, Detail: fmt.Sprintf("available %d/%d", s.AvailableReplicas, want)}
}

func (p *provider) daemonSetHealth(ctx context.Context, d *appsv1.DaemonSet) tool.Health {
	if d.Generation > d.Status.ObservedGeneration {
		return tool.Health{Detail: "waiting for the controller"}
	}
	s := d.Status
	want := s.DesiredNumberScheduled
	if d.Spec.UpdateStrategy.Type != appsv1.OnDeleteDaemonSetStrategyType && s.UpdatedNumberScheduled < want {
		return tool.Health{Detail: p.withPodReason(ctx, d.Namespace, d.Spec.Selector, fmt.Sprintf("updated %d/%d", s.UpdatedNumberScheduled, want))}
	}
	if s.NumberAvailable < want {
		return tool.Health{Detail: p.withPodReason(ctx, d.Namespace, d.Spec.Selector, fmt.Sprintf("available %d/%d", s.NumberAvailable, want))}
	}
	return tool.Health{Done: true, Healthy: true, Detail: fmt.Sprintf("available %d/%d", s.NumberAvailable, want)}
}

// podReplacementCheck waits until the deleted pod is gone and the workload
// that owns it is fully available again.
func (p *provider) podReplacementCheck(ns, name string, uid types.UID, owner *metav1.OwnerReference) tool.Check {
	if owner == nil {
		return nil
	}
	return func(ctx context.Context) (tool.Health, error) {
		pod, err := p.client.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil && pod.UID == uid:
			return tool.Health{Detail: "waiting for the old pod to stop"}, nil
		case err != nil && !apierrors.IsNotFound(err):
			return tool.Health{}, err
		}
		kind, workload := "", owner.Name
		switch owner.Kind {
		case "ReplicaSet":
			rs, err := p.client.AppsV1().ReplicaSets(ns).Get(ctx, owner.Name, metav1.GetOptions{})
			if err != nil {
				return tool.Health{}, err
			}
			parent := metav1.GetControllerOf(rs)
			if parent == nil || parent.Kind != "Deployment" {
				return replicaSetHealth(rs), nil
			}
			kind, workload = "deployment", parent.Name
		case "StatefulSet":
			kind = "statefulset"
		case "DaemonSet":
			kind = "daemonset"
		default:
			// Jobs and other controllers have no availability to wait for.
			return tool.Health{Done: true, Healthy: true, Detail: "old pod removed"}, nil
		}
		return p.workloadCheck(kind, ns, workload)(ctx)
	}
}

func replicaSetHealth(rs *appsv1.ReplicaSet) tool.Health {
	want := int32(1)
	if rs.Spec.Replicas != nil {
		want = *rs.Spec.Replicas
	}
	if rs.Status.AvailableReplicas < want {
		return tool.Health{Detail: fmt.Sprintf("available %d/%d", rs.Status.AvailableReplicas, want)}
	}
	return tool.Health{Done: true, Healthy: true, Detail: fmt.Sprintf("available %d/%d", rs.Status.AvailableReplicas, want)}
}

// withPodReason appends the first container waiting or termination reason
// among the workload's unready pods, such as CrashLoopBackOff, so a person
// sees why the rollout is stuck without opening logs.
func (p *provider) withPodReason(ctx context.Context, ns string, selector *metav1.LabelSelector, detail string) string {
	if selector == nil {
		return detail
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil || sel.Empty() {
		return detail
	}
	pods, err := p.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String(), Limit: 50})
	if err != nil {
		return detail
	}
	for i := range pods.Items {
		if reason := podReason(&pods.Items[i]); reason != "" {
			return detail + ", " + reason
		}
	}
	return detail
}

func podReason(pod *corev1.Pod) string {
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for _, c := range statuses {
			var reason string
			switch {
			case c.State.Waiting != nil:
				reason = c.State.Waiting.Reason
			case c.State.Terminated != nil && c.State.Terminated.ExitCode != 0:
				reason = c.State.Terminated.Reason
			}
			if reason != "" && reason != "ContainerCreating" && reason != "PodInitializing" {
				return safeReason(reason)
			}
		}
	}
	return ""
}

// safeReason keeps only a short CamelCase word: reasons come from the
// cluster and are shown on approval pages and in notifications.
func safeReason(s string) string {
	if len(s) > 48 {
		return ""
	}
	if strings.IndexFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') }) >= 0 {
		return ""
	}
	return s
}
