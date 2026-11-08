package kube

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/tool"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kt "k8s.io/client-go/testing"
)

func TestScaleUndo(t *testing.T) {
	p, client := writeProvider()
	p.cfg.Name = "main"
	current := int32(3)
	client.PrependReactor("get", "*", func(kt.Action) (bool, runtime.Object, error) {
		return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "u", ResourceVersion: "1"}, Spec: autoscalingv1.ScaleSpec{Replicas: current}}, nil
	})
	ctx := context.Background()
	plan, err := p.planScale(ctx, tool.Caller{}, scaleInput{Kind: "deployment", Namespace: "prod", Name: "web", Replicas: 5})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"cluster": "main", "kind": "deployment", "namespace": "prod", "name": "web", "replicas": int32(3)}
	if plan.Undo == nil || plan.Undo.Tool != "k8s_scale" || !reflect.DeepEqual(plan.Undo.Args, want) {
		t.Fatalf("undo %+v", plan.Undo)
	}
	if err := action.Validate(plan.Undo.Tool, plan.Undo.Args); err != nil {
		t.Fatalf("undo is not a catalog link: %v", err)
	}
	// After the scale, the undo's own plan offers the way back again.
	current = 5
	back, err := p.planScale(ctx, tool.Caller{}, scaleInput{Kind: "deployment", Namespace: "prod", Name: "web", Replicas: 3})
	if err != nil || back.Undo == nil || back.Undo.Args["replicas"] != int32(5) {
		t.Fatalf("undo of undo %+v %v", back.Undo, err)
	}
	same, err := p.planScale(ctx, tool.Caller{}, scaleInput{Kind: "deployment", Namespace: "prod", Name: "web", Replicas: 5})
	if err != nil || same.Undo != nil {
		t.Fatalf("no-op scale offered undo %+v %v", same.Undo, err)
	}
}

func TestRestoreImageUndoAndRevert(t *testing.T) {
	p, client, in := restoreFixture()
	ctx := context.Background()
	plan, err := p.planRestoreImage(ctx, tool.Caller{}, in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"cluster": "main", "namespace": "prod", "name": "web", "container": "app", "expected_image": in.Image, "image": in.ExpectedImage, "revert": true}
	if plan.Undo == nil || plan.Undo.Tool != "k8s_restore_image" || !reflect.DeepEqual(plan.Undo.Args, want) {
		t.Fatalf("undo %+v", plan.Undo)
	}
	if err := action.Validate(plan.Undo.Tool, plan.Undo.Args); err != nil {
		t.Fatalf("undo is not a catalog link: %v", err)
	}
	if _, err := p.applyRestoreImage(ctx, tool.Caller{}, in, plan); err != nil {
		t.Fatal(err)
	}

	// The undo returns to the failing image: no ready Pod runs it, but the
	// Deployment's history does, so only revert mode accepts it.
	revert := restoreImageInput{Namespace: "prod", Name: "web", Container: "app", ExpectedImage: in.Image, Image: in.ExpectedImage, Revert: true}
	normal := revert
	normal.Revert = false
	if _, err := p.planRestoreImage(ctx, tool.Caller{}, normal); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("unready image accepted without revert: %v", err)
	}
	back, err := p.planRestoreImage(ctx, tool.Caller{}, revert)
	if err != nil || !strings.Contains(back.Summary, "NOT verified healthy") {
		t.Fatalf("%+v %v", back, err)
	}
	if back.Undo == nil || back.Undo.Args["revert"] != nil || back.Undo.Args["image"] != in.Image {
		t.Fatalf("undo of revert must be an ordinary restore: %+v", back.Undo)
	}
	if _, err := p.applyRestoreImage(ctx, tool.Caller{}, revert, back); err != nil {
		t.Fatal(err)
	}
	d, _ := client.AppsV1().Deployments("prod").Get(ctx, "web", metav1.GetOptions{})
	if d.Spec.Template.Spec.Containers[0].Image != in.ExpectedImage {
		t.Fatalf("image %q", d.Spec.Template.Spec.Containers[0].Image)
	}

	for name, image := range map[string]string{"never used": "registry.example/app:other", "other container": "sidecar:keep"} {
		bad := restoreImageInput{Namespace: "prod", Name: "web", Container: "app", ExpectedImage: in.ExpectedImage, Image: image, Revert: true}
		if _, err := p.planRestoreImage(ctx, tool.Caller{}, bad); !errors.Is(err, tool.ErrDenied) {
			t.Fatalf("%s: revert accepted an image outside the history: %v", name, err)
		}
	}
}

func TestRevertIgnoresDeletingAndForeignReplicaSets(t *testing.T) {
	for _, scenario := range []string{"deleting", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			p, client, in := restoreFixture()
			ctx := context.Background()
			rs, _ := client.AppsV1().ReplicaSets("prod").Get(ctx, "web-good", metav1.GetOptions{})
			if scenario == "deleting" {
				at := metav1.Now()
				rs.DeletionTimestamp = &at
			} else {
				rs.OwnerReferences[0].UID = "other"
			}
			_, _ = client.AppsV1().ReplicaSets("prod").Update(ctx, rs, metav1.UpdateOptions{})
			revert := restoreImageInput{Namespace: "prod", Name: "web", Container: "app", ExpectedImage: in.ExpectedImage, Image: in.Image, Revert: true}
			if _, err := p.planRestoreImage(ctx, tool.Caller{}, revert); !errors.Is(err, tool.ErrDenied) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}
