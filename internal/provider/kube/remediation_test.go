package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func restoreFixture() (*provider, *fake.Clientset, restoreImageInput) {
	controller := true
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "deployment-uid", ResourceVersion: "1"}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/app:bad"}, {Name: "sidecar", Image: "sidecar:keep"}}}}}}
	owner := []metav1.OwnerReference{{Kind: "Deployment", Name: "web", UID: d.UID, Controller: &controller}}
	good := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "web-good", Namespace: "prod", UID: "good-uid", OwnerReferences: owner}, Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/app:good"}}}}}, Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1}}
	bad := good.DeepCopy()
	bad.Name = "web-bad"
	bad.UID = "bad-uid"
	bad.Status.ReadyReplicas = 0
	bad.Spec.Template.Spec.Containers[0].Image = "registry.example/app:bad"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-failed", Namespace: "prod", UID: "pod-uid", OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: bad.Name, UID: bad.UID, Controller: &controller}}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "registry.example/app:bad"}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}}}}}
	ready := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-ready", Namespace: "prod", UID: "ready-uid", OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: good.Name, UID: good.UID, Controller: &controller}}}, Spec: good.Spec.Template.Spec, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}, ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Ready: true, Image: "registry.example/app:good", ImageID: "containerd://sha256:" + strings.Repeat("a", 64), State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	p, client := writeProvider(d, good, bad, pod, ready)
	p.cfg.RestoreImage = []string{"prod/web"}
	p.cfg.Name = "main"
	p.cfg.ActionApprovalURL = "https://ops.example"
	return p, client, restoreImageInput{Namespace: "prod", Name: "web", Container: "app", ExpectedImage: "registry.example/app:bad", Image: "registry.example/app:good"}
}

func TestRestoreRequiresReadyOwnedRunningImage(t *testing.T) {
	for _, scenario := range []string{"missing pod", "wrong owner", "wrong kind", "deleting pod", "pod unready", "container unready", "not running", "stale template", "wrong status image", "missing imageID", "deleting RS"} {
		t.Run(scenario, func(t *testing.T) {
			p, client, in := restoreFixture()
			ctx := context.Background()
			approved, err := p.planRestoreImage(ctx, tool.Caller{}, in)
			if err != nil {
				t.Fatal(err)
			}
			pod, _ := client.CoreV1().Pods("prod").Get(ctx, "web-ready", metav1.GetOptions{})
			switch scenario {
			case "missing pod":
				_ = client.CoreV1().Pods("prod").Delete(ctx, pod.Name, metav1.DeleteOptions{})
			case "wrong owner":
				pod.OwnerReferences[0].UID = "other"
			case "wrong kind":
				pod.OwnerReferences[0].Kind = "Deployment"
			case "deleting pod":
				at := metav1.NewTime(time.Now())
				pod.DeletionTimestamp = &at
			case "pod unready":
				pod.Status.Conditions[0].Status = corev1.ConditionFalse
			case "container unready":
				pod.Status.ContainerStatuses[0].Ready = false
			case "not running":
				pod.Status.ContainerStatuses[0].State = corev1.ContainerState{}
			case "stale template":
				pod.Spec.Containers[0].Image = "registry.example/app:old"
			case "wrong status image":
				pod.Status.ContainerStatuses[0].Image = "registry.example/app:old"
			case "missing imageID":
				pod.Status.ContainerStatuses[0].ImageID = ""
			case "deleting RS":
				rs, _ := client.AppsV1().ReplicaSets("prod").Get(ctx, "web-good", metav1.GetOptions{})
				at := metav1.NewTime(time.Now())
				rs.DeletionTimestamp = &at
				_, _ = client.AppsV1().ReplicaSets("prod").Update(ctx, rs, metav1.UpdateOptions{})
			}
			if scenario != "missing pod" {
				_, _ = client.CoreV1().Pods("prod").Update(ctx, pod, metav1.UpdateOptions{})
			}
			client.ClearActions()
			if _, err := p.planRestoreImage(ctx, tool.Caller{}, in); err == nil {
				t.Error("unsafe source planned")
			}
			if _, err := p.applyRestoreImage(ctx, tool.Caller{}, in, approved); err == nil {
				t.Error("unsafe source applied")
			}
			for _, a := range client.Actions() {
				if a.GetVerb() == "patch" {
					t.Error("refused restore patched Deployment")
				}
			}
		})
	}
}

func TestRestoreReportedImageIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		template, reported, id string
		ok                     bool
	}{
		{"nginx", "docker.io/library/nginx:latest", "containerd://" + digest, true},
		{"nginx:1.27", "index.docker.io/library/nginx:1.27", "docker-pullable://docker.io/library/nginx@" + digest, true},
		{"registry.example/app@" + digest, "registry.example/app@" + digest, "containerd://" + digest, true},
		{"registry.example/app@" + digest, "registry.example/app@" + digest, "containerd://" + other, false},
		{"registry.example/app:good", "registry.example/app:other", "containerd://" + digest, false},
		{"index.evil.example/app:good", "evil.example/app:good", "containerd://" + digest, false},
		{"nginx:good", "nginx:good", "opaque-runtime-id", false},
	} {
		if got := reportedImageMatches(tc.template, tc.reported, tc.id); got != tc.ok {
			t.Errorf("%q / %q / %q: %v", tc.template, tc.reported, tc.id, got)
		}
	}
}
func TestVerifiedRestoreOptionsAndApply(t *testing.T) {
	p, client, in := restoreFixture()
	ctx := context.Background()
	raw, err := p.remediation(ctx, tool.Caller{}, remediationInput{Namespace: "prod", Name: "web-failed"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Actions []imageRestoreOption `json:"actions"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Actions) != 1 || !strings.HasPrefix(result.Actions[0].URL, "https://ops.example/approve/action/k8s_restore_image?") || result.Actions[0].Image != in.Image {
		t.Fatal(raw)
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" {
			t.Fatal("read-only options wrote to cluster")
		}
	}
	plan, err := p.planRestoreImage(ctx, tool.Caller{}, in)
	if err != nil || !plan.NoWindow {
		t.Fatal(err)
	}
	if _, err := p.applyRestoreImage(ctx, tool.Caller{}, in, plan); err != nil {
		t.Fatal(err)
	}
	deployment, err := client.AppsV1().Deployments("prod").Get(ctx, "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != in.Image || deployment.Spec.Template.Spec.Containers[1].Image != "sidecar:keep" {
		t.Fatal("restored more than the approved image")
	}
	if _, err := p.applyRestoreImage(ctx, tool.Caller{}, in, plan); err == nil {
		t.Fatal("replayed image transition applied")
	}
}
func TestImageRestoreRefusals(t *testing.T) {
	for _, scenario := range []string{"disabled", "namespace", "stale image", "missing healthy source", "recreated Deployment", "different target", "no plan"} {
		t.Run(scenario, func(t *testing.T) {
			p, client, in := restoreFixture()
			ctx := context.Background()
			plan, err := p.planRestoreImage(ctx, tool.Caller{}, in)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "disabled":
				p.cfg.RestoreImage = nil
			case "namespace":
				p.policy.deny = []string{"prod"}
			case "stale image":
				d, _ := client.AppsV1().Deployments("prod").Get(ctx, "web", metav1.GetOptions{})
				d.Spec.Template.Spec.Containers[0].Image = "newer:release"
				_, _ = client.AppsV1().Deployments("prod").Update(ctx, d, metav1.UpdateOptions{})
			case "missing healthy source":
				rs, _ := client.AppsV1().ReplicaSets("prod").Get(ctx, "web-good", metav1.GetOptions{})
				rs.Status.ReadyReplicas = 0
				_, _ = client.AppsV1().ReplicaSets("prod").UpdateStatus(ctx, rs, metav1.UpdateOptions{})
			case "recreated Deployment":
				d, _ := client.AppsV1().Deployments("prod").Get(ctx, "web", metav1.GetOptions{})
				d.UID = types.UID("replacement")
				_, _ = client.AppsV1().Deployments("prod").Update(ctx, d, metav1.UpdateOptions{})
			case "different target":
				in.Image = "unverified:latest"
			case "no plan":
				plan = tool.Plan{}
			}
			client.ClearActions()
			if _, err := p.applyRestoreImage(ctx, tool.Caller{}, in, plan); err == nil {
				t.Fatal("unsafe restore accepted")
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "patch" {
					t.Fatal("refused action patched cluster")
				}
			}
		})
	}
}

func TestImageRestoreThroughClusterWrapper(t *testing.T) {
	p, client, in := restoreFixture()
	in.Cluster = "main"
	r := &registry{byName: map[string]*provider{"main": p}}
	handler := clusterWrite(r, (*provider).planRestoreImage, (*provider).applyRestoreImage)
	plan, err := handler.Plan(context.Background(), tool.Caller{}, in)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NoWindow || !strings.HasPrefix(plan.Summary, `Cluster "main": `) {
		t.Fatalf("wrapped plan: %+v", plan)
	}
	if _, err := handler.Apply(context.Background(), tool.Caller{}, in, plan); err != nil {
		t.Fatalf("approved wrapped plan refused: %v", err)
	}
	deployment, err := client.AppsV1().Deployments("prod").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil || deployment.Spec.Template.Spec.Containers[0].Image != in.Image {
		t.Fatalf("image restore not applied: %v", err)
	}
	if _, err := handler.Apply(context.Background(), tool.Caller{}, in, plan); err == nil {
		t.Fatal("stale plan accepted")
	}
}
