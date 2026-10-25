package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func writeProvider(objects ...runtime.Object) (*provider, *fake.Clientset) {
	client := fake.NewClientset(objects...)
	cfg := config.Kubernetes{Namespaces: []string{"*"}, DenyNamespaces: []string{"private*"}, RolloutRestart: []string{"prod/web*"}, Scale: []string{"prod/web*"}, DeletePod: []string{"prod/web*"}, MaxReplicas: 10}
	return &provider{client: client, cfg: cfg, policy: namespacePolicy{cfg.Namespaces, cfg.DenyNamespaces}}, client
}

func TestWritePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, ns, target      string
		allow, deny, patterns []string
		ok                    bool
	}{
		{"allowed", "prod", "web", []string{"*"}, nil, []string{"prod/web*"}, true},
		{"empty action", "prod", "web", []string{"*"}, nil, nil, false},
		{"empty namespace policy", "prod", "web", nil, nil, []string{"*/*"}, false},
		{"deny wins", "prod", "web", []string{"*"}, []string{"prod"}, []string{"*/*"}, false},
		{"namespace miss", "dev", "web", []string{"prod"}, nil, []string{"*/*"}, false},
		{"name miss", "prod", "api", []string{"*"}, nil, []string{"prod/web*"}, false},
		{"glob", "prod", "web-1", []string{"pr*"}, nil, []string{"prod/web-?"}, true},
		{"slash", "prod", "web/x", []string{"*"}, nil, []string{"*/*"}, false},
		{"missing ns", "", "web", []string{"*"}, nil, []string{"*/*"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, client := writeProvider()
			p.policy = namespacePolicy{tc.allow, tc.deny}
			p.cfg.RolloutRestart, p.cfg.Scale, p.cfg.DeletePod = tc.patterns, tc.patterns, tc.patterns
			err := p.writeAllowed(tc.ns, tc.target, tc.patterns)
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
			if tc.ok {
				return
			}
			in := rolloutInput{Kind: "deployment", Namespace: tc.ns, Name: tc.target}
			if _, err = p.planRollout(context.Background(), tool.Caller{}, in); err == nil {
				t.Fatal("Plan accepted")
			}
			if _, err = p.applyRollout(context.Background(), tool.Caller{}, in); err == nil {
				t.Fatal("Apply accepted")
			}
			scale := scaleInput{Kind: "deployment", Namespace: tc.ns, Name: tc.target, Replicas: 1}
			if _, err = p.planScale(context.Background(), tool.Caller{}, scale); err == nil {
				t.Fatal("Plan accepted")
			}
			if _, err = p.applyScale(context.Background(), tool.Caller{}, scale); err == nil {
				t.Fatal("Apply accepted")
			}
			pod := deletePodInput{Namespace: tc.ns, Name: tc.target}
			if _, err = p.planDeletePod(context.Background(), tool.Caller{}, pod); err == nil {
				t.Fatal("Plan accepted")
			}
			if _, err = p.applyDeletePod(context.Background(), tool.Caller{}, pod); err == nil {
				t.Fatal("Apply accepted")
			}
			if len(client.Actions()) != 0 {
				t.Fatal("denial contacted API", client.Actions())
			}
		})
	}
}

func TestRollout(t *testing.T) {
	for _, kind := range []string{"deployment", "statefulset", "daemonset"} {
		t.Run(kind, func(t *testing.T) {
			count := int32(3)
			md := metav1.ObjectMeta{Name: "web", Namespace: "prod"}
			template := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:1"}}}}
			var obj runtime.Object
			switch kind {
			case "deployment":
				obj = &appsv1.Deployment{ObjectMeta: md, Spec: appsv1.DeploymentSpec{Replicas: &count, Template: template}, Status: appsv1.DeploymentStatus{ReadyReplicas: 2}}
			case "statefulset":
				obj = &appsv1.StatefulSet{ObjectMeta: md, Spec: appsv1.StatefulSetSpec{Replicas: &count, Template: template}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 2}}
			case "daemonset":
				obj = &appsv1.DaemonSet{ObjectMeta: md, Spec: appsv1.DaemonSetSpec{Template: template}, Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 2}}
			}
			p, client := writeProvider(obj)
			in := rolloutInput{Kind: kind, Namespace: "prod", Name: "web"}
			plan, err := p.planRollout(context.Background(), tool.Caller{}, in)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{kind, "prod/web", "replicas 3", "ready 2", "nginx:1"} {
				if !strings.Contains(plan.Summary, want) {
					t.Fatal(plan)
				}
			}
			if len(client.Actions()) != 1 || client.Actions()[0].GetVerb() != "get" {
				t.Fatal("Plan mutated", client.Actions())
			}
			before := time.Now().Add(-time.Second)
			if _, err = p.applyRollout(context.Background(), tool.Caller{}, in); err != nil {
				t.Fatal(err)
			}
			action := client.Actions()[1].(kt.PatchAction)
			if action.GetPatchType() != types.StrategicMergePatchType {
				t.Fatal(action)
			}
			var body struct {
				Spec struct {
					Template struct {
						Metadata struct{ Annotations map[string]string }
					}
				}
			}
			if err = json.Unmarshal(action.GetPatch(), &body); err != nil {
				t.Fatal(err)
			}
			stamp, err := time.Parse(time.RFC3339, body.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"])
			if err != nil || stamp.Before(before) || stamp.After(time.Now()) {
				t.Fatalf("%s %v", action.GetPatch(), err)
			}
		})
	}
}

func TestScaleBoundsAndKinds(t *testing.T) {
	for _, replicas := range []int32{-1, 0, 10, 11} {
		p, client := writeProvider()
		err := p.checkScale(scaleInput{Kind: "deployment", Namespace: "prod", Name: "web", Replicas: replicas})
		if (err == nil) != (replicas >= 0 && replicas <= 10) {
			t.Fatal(replicas, err)
		}
		if len(client.Actions()) != 0 {
			t.Fatal(client.Actions())
		}
	}
	for _, kind := range []string{"daemonset", "Deployment", "deploy", "pod", ""} {
		if _, err := writeKind(kind, false); err == nil {
			t.Fatal(kind)
		}
	}
}

func TestScale(t *testing.T) {
	for _, kind := range []string{"deployment", "statefulset"} {
		for _, change := range []string{"none", "uid", "version"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				p, client := writeProvider()
				scale := &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "original", ResourceVersion: "7"}, Spec: autoscalingv1.ScaleSpec{Replicas: 2}}
				client.PrependReactor("get", "*", func(a kt.Action) (bool, runtime.Object, error) {
					if a.GetSubresource() != "scale" {
						t.Fatal(a)
					}
					return true, scale.DeepCopy(), nil
				})
				updates := 0
				client.PrependReactor("update", "*", func(a kt.Action) (bool, runtime.Object, error) {
					updates++
					got := a.(kt.UpdateAction).GetObject().(*autoscalingv1.Scale)
					if a.GetSubresource() != "scale" || got.ResourceVersion != "7" || got.UID != "original" || got.Spec.Replicas != 4 {
						t.Fatal(a, got)
					}
					return true, got, nil
				})
				in := scaleInput{Kind: kind, Namespace: "prod", Name: "web", Replicas: 4}
				caller := tool.Caller{User: "alice", Transport: "http"}
				plan, err := p.planScale(context.Background(), caller, in)
				if err != nil || !strings.Contains(plan.Summary, "2 → 4") {
					t.Fatalf("%+v %v", plan, err)
				}
				if len(client.Actions()) != 1 {
					t.Fatal(client.Actions())
				}
				if change == "uid" {
					scale.UID = "replacement"
				}
				if change == "version" {
					scale.ResourceVersion = "8"
				}
				_, err = p.applyScale(context.Background(), caller, in)
				if change == "none" {
					if err != nil || updates != 1 {
						t.Fatal(err, updates)
					}
				} else if !errors.Is(err, tool.ErrDenied) || updates != 0 {
					t.Fatal(err, updates)
				}
				if _, err = p.applyScale(context.Background(), caller, in); !errors.Is(err, tool.ErrDenied) {
					t.Fatal("preview reused", err)
				}
			})
		}
	}
}

func TestDeletePod(t *testing.T) {
	for _, owned := range []bool{false, true} {
		for _, recreated := range []bool{false, true} {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: "original", ResourceVersion: "7"}}
			controller := true
			if owned {
				pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-rs", Controller: &controller}}
			}
			p, client := writeProvider(pod)
			in := deletePodInput{Namespace: "prod", Name: "web"}
			plan, err := p.planDeletePod(context.Background(), tool.Caller{}, in)
			if err != nil {
				t.Fatal(err)
			}
			if owned && !strings.Contains(plan.Summary, `controller owner "ReplicaSet"/"web-rs"`) || !owned && !strings.Contains(plan.Summary, "will NOT be recreated") {
				t.Fatal(plan)
			}
			if len(client.Actions()) != 1 {
				t.Fatal("Plan mutated", client.Actions())
			}
			if recreated {
				replacement := pod.DeepCopy()
				replacement.UID = "replacement"
				if err = client.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), replacement, "prod"); err != nil {
					t.Fatal(err)
				}
			}
			_, err = p.applyDeletePod(context.Background(), tool.Caller{}, in)
			if recreated {
				if !errors.Is(err, tool.ErrDenied) || len(client.Actions()) != 2 {
					t.Fatal(err, client.Actions())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				options := client.Actions()[2].(kt.DeleteAction).GetDeleteOptions()
				if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "original" {
					t.Fatal(options)
				}
			}
		}
	}
}

func TestSnapshots(t *testing.T) {
	var snapshots writeSnapshots
	key := writeKey{user: "alice", name: "web"}
	if _, err := snapshots.take(key); !errors.Is(err, tool.ErrDenied) {
		t.Fatal(err)
	}
	if _, err := snapshots.remember(key, "uid", "1", "summary"); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshots.remember(key, "other", "1", "summary"); !errors.Is(err, tool.ErrDenied) {
		t.Fatal(err)
	}
	if _, err := snapshots.take(writeKey{user: "bob", name: "web"}); !errors.Is(err, tool.ErrDenied) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = snapshots.remember(key, "uid", "1", "summary") })
	}
	wg.Wait()
	if _, err := snapshots.take(key); err != nil {
		t.Fatal(err)
	}
	snapshots.pending[key] = writeSnapshot{expires: time.Now().Add(-time.Second)}
	if _, err := snapshots.take(key); !errors.Is(err, tool.ErrDenied) {
		t.Fatal(err)
	}
	for i := range 1024 {
		key.replicas = int32(i)
		if _, err := snapshots.remember(key, "uid", "1", "summary"); err != nil {
			t.Fatal(err)
		}
	}
	key.replicas = 1024
	if _, err := snapshots.remember(key, "uid", "1", "summary"); !errors.Is(err, tool.ErrDenied) {
		t.Fatal(err)
	}
}
