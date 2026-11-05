package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
	p, client := writeProvider(d, good, bad, pod)
	p.cfg.RestoreImage = []string{"prod/web"}
	p.cfg.Name = "main"
	p.cfg.ActionApprovalURL = "https://ops.example"
	return p, client, restoreImageInput{Namespace: "prod", Name: "web", Container: "app", ExpectedImage: "registry.example/app:bad", Image: "registry.example/app:good"}
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
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.Actions) != 1 || !strings.HasPrefix(result.Actions[0].URL, "https://ops.example/approve/restore-image/main/prod/web?") || result.Actions[0].Image != in.Image {
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
