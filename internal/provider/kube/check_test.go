package kube

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func checkReplicas(n int32) *int32 { return &n }

func checkDeployment(generation, observed int64, status appsv1.DeploymentStatus, conditions ...appsv1.DeploymentCondition) *appsv1.Deployment {
	status.ObservedGeneration = observed
	status.Conditions = conditions
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps", Generation: generation},
		Spec:       appsv1.DeploymentSpec{Replicas: checkReplicas(2), Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
		Status:     status,
	}
}

func crashingPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "apps", Labels: map[string]string{"app": "web"}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app",
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "secret=hunter2"}}}}},
	}
}

func TestDeploymentRecoveryCheck(t *testing.T) {
	for _, tc := range []struct {
		name          string
		objects       []runtime.Object
		done, healthy bool
		detail        string
	}{
		{"controller has not seen the restart", []runtime.Object{checkDeployment(3, 2, appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2})}, false, false, "waiting for the controller"},
		{"rolling with a crashing pod", []runtime.Object{checkDeployment(3, 3, appsv1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 1, AvailableReplicas: 2}), crashingPod()}, false, false, "updated 1/2, CrashLoopBackOff"},
		{"old pods still terminating", []runtime.Object{checkDeployment(3, 3, appsv1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 2, AvailableReplicas: 2})}, false, false, "1 old pods terminating"},
		{"new pods not available", []runtime.Object{checkDeployment(3, 3, appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 1})}, false, false, "available 1/2"},
		{"progress deadline", []runtime.Object{checkDeployment(3, 3, appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 1},
			appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}), crashingPod()}, true, false, "rollout exceeded its progress deadline, CrashLoopBackOff"},
		{"recovered", []runtime.Object{checkDeployment(3, 3, appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2})}, true, true, "available 2/2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := testProvider(tc.objects...).workloadCheck("deployment", "apps", "web")(context.Background())
			if err != nil || h.Done != tc.done || h.Healthy != tc.healthy || h.Detail != tc.detail {
				t.Fatalf("health %+v, err %v", h, err)
			}
			if strings.Contains(h.Detail, "hunter2") {
				t.Fatal("pod message leaked into the detail")
			}
		})
	}
}

func TestStatefulSetAndDaemonSetRecoveryCheck(t *testing.T) {
	st := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "apps", Generation: 2},
		Spec:       appsv1.StatefulSetSpec{Replicas: checkReplicas(1)},
		Status:     appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, CurrentRevision: "a", UpdateRevision: "b"},
	}
	p := testProvider(st)
	if h, _ := p.workloadCheck("statefulset", "apps", "db")(context.Background()); h.Healthy || h.Detail != "updated 1/1" {
		t.Fatalf("revision change not finished: %+v", h)
	}
	st.Status.CurrentRevision = "b"
	if h, _ := testProvider(st).workloadCheck("statefulset", "apps", "db")(context.Background()); !h.Healthy || !h.Done {
		t.Fatalf("statefulset: %+v", h)
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "apps", Generation: 1},
		Status:     appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 3, UpdatedNumberScheduled: 3, NumberAvailable: 2},
	}
	if h, _ := testProvider(ds).workloadCheck("daemonset", "apps", "agent")(context.Background()); h.Healthy || h.Detail != "available 2/3" {
		t.Fatalf("daemonset: %+v", h)
	}
}

func TestPodReplacementCheck(t *testing.T) {
	controller := true
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "web-abc", Namespace: "apps",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: &controller}}}}
	old := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-abc-1", Namespace: "apps", UID: "old"}}
	owner := &metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-abc", Controller: &controller}
	healthy := checkDeployment(1, 1, appsv1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2})

	if check := testProvider().podReplacementCheck("apps", "web-abc-1", "old", nil); check != nil {
		t.Fatal("a pod without a controller has nothing to wait for")
	}
	h, err := testProvider(old, rs, healthy).podReplacementCheck("apps", "web-abc-1", "old", owner)(context.Background())
	if err != nil || h.Healthy || h.Detail != "waiting for the old pod to stop" {
		t.Fatalf("old pod still present: %+v %v", h, err)
	}
	h, err = testProvider(rs, healthy).podReplacementCheck("apps", "web-abc-1", "old", owner)(context.Background())
	if err != nil || !h.Healthy || h.Detail != "available 2/2" {
		t.Fatalf("replaced: %+v %v", h, err)
	}
}

func TestSafeReason(t *testing.T) {
	for in, want := range map[string]string{"CrashLoopBackOff": "CrashLoopBackOff", "OOMKilled": "OOMKilled", "bad reason": "", "x\ny": "", strings.Repeat("A", 49): ""} {
		if got := safeReason(in); got != want {
			t.Errorf("safeReason(%q) = %q", in, got)
		}
	}
}
