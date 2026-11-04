package kube

import (
	"path"
	"strings"

	"github.com/gkdis6/opsgate/internal/alert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Hints are fixed strings so text from the cluster can never pose as advice.
const (
	hintImage         = "Check the image name, tag and registry access. A restart will not help."
	hintConfig        = "Check that the Secrets and ConfigMaps this pod references exist. A restart will not help."
	hintContainer     = "Check the pod events for the container command, mounts and security settings."
	hintOOM           = "The container hit its memory limit. Check the limit and the app's usage; a restart will not prevent a repeat."
	hintUnknownExit   = "An Unknown exit usually means the node or container runtime restarted. If many pods restarted together, check the node."
	hintCompleted     = "The process exited with status 0. Check whether it is meant to stay running."
	hintLogs          = "Check the previous container logs for the exit cause."
	hintJob           = "This is a Job pod. Check its logs; rerun it by hand only if repeating its work is safe."
	hintEvicted       = "The node evicted this pod, usually under resource pressure. Check the node."
	hintFailed        = "Check the pod's events and logs."
	hintUnschedulable = "Check node capacity and taints against the pod's requests and selectors."
	hintStalled       = "New pods are not becoming ready. Check their events and logs, and roll back if the release is bad. A restart will not help."
	hintNode          = "Check the host and kubelet. Pods on this node may be affected."
)

// repeatedRestarts is when a restart is worth suggesting. Kubernetes already
// restarts a crashed container, so the suggestion is for one that keeps failing.
const repeatedRestarts = 3

// podOwner returns the lower-case kind and name of the pod's controller, with a
// ReplicaSet resolved to its Deployment through the pod template hash.
func podOwner(p *corev1.Pod) (kind, name string) {
	owner := metav1.GetControllerOfNoCopy(p)
	if owner == nil {
		return "", ""
	}
	if hash := p.Labels[appsv1.DefaultDeploymentUniqueLabelKey]; owner.Kind == "ReplicaSet" && hash != "" {
		if deployment, ok := strings.CutSuffix(owner.Name, "-"+hash); ok && deployment != "" {
			return "deployment", deployment
		}
	}
	return strings.ToLower(owner.Kind), owner.Name
}

// restartAdvice returns the hint for a container restart and whether replacing
// the pods could plausibly help.
func restartAdvice(reason, owner string) (hint string, restartable bool) {
	switch reason {
	case "OOMKilled":
		return hintOOM, false
	case "Unknown", "ContainerStatusUnknown":
		return hintUnknownExit, false
	case "Completed":
		return hintCompleted, false
	}
	if owner == "job" {
		return hintJob, false
	}
	return hintLogs, true
}

func waitingHint(reason string) string {
	switch reason {
	case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
		return hintImage
	case "CreateContainerConfigError":
		return hintConfig
	}
	return hintContainer
}

func failedHint(reason, owner string) string {
	switch {
	case reason == "Evicted":
		return hintEvicted
	case owner == "job":
		return hintJob
	}
	return hintFailed
}

// restartTarget returns the workload if rollout_restart allows it. The tool
// checks the allowlist again and still needs human approval.
func (w *watcher) restartTarget(kind, namespace, name string) *alert.Workload {
	switch kind {
	case "deployment", "statefulset", "daemonset":
	default:
		return nil
	}
	for _, pattern := range w.restart {
		if ok, _ := path.Match(pattern, namespace+"/"+name); ok {
			return &alert.Workload{Kind: kind, Namespace: namespace, Name: name}
		}
	}
	return nil
}
