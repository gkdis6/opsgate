package kube

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/alert"
	"github.com/gkdis6/opsgate/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func testWatcher() *watcher {
	return &watcher{cluster: "main", policy: namespacePolicy{allow: []string{"*"}}, now: time.Now}
}

func restartingPod(restarts int32, reason string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "apps"}}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", RestartCount: restarts}}
	if reason != "" {
		p.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 137}
	}
	return p
}

func reasons(alerts []alert.Alert) []string {
	var result []string
	for _, a := range alerts {
		result = append(result, a.Severity+":"+a.Reason)
	}
	return result
}

func TestPodAlerts(t *testing.T) {
	w := testWatcher()
	got := w.pod(restartingPod(1, ""), restartingPod(2, "OOMKilled"))
	if !reflect.DeepEqual(reasons(got), []string{"warning:OOMKilled"}) || !strings.Contains(got[0].Summary, "container app restarted (2 restarts): OOMKilled, exit code 137") {
		t.Fatalf("restart: %+v", got)
	}
	if got[0].Key != "Pod/apps/web-1/app/restart" {
		t.Fatalf("key = %q", got[0].Key)
	}
	if got := w.pod(restartingPod(2, "OOMKilled"), restartingPod(2, "OOMKilled")); len(got) != 0 {
		t.Fatalf("unchanged pod alerted: %+v", got)
	}
	if got := w.pod(nil, restartingPod(5, "Error")); len(got) != 0 {
		t.Fatalf("new pod alerted: %+v", got)
	}

	pulling := restartingPod(0, "")
	pulling.Status.ContainerStatuses[0].State.Waiting = &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull access denied"}
	if got := w.pod(restartingPod(0, ""), pulling); !reflect.DeepEqual(reasons(got), []string{"warning:ImagePullBackOff"}) {
		t.Fatalf("waiting: %+v", got)
	}
	if got := w.pod(pulling, pulling); len(got) != 0 {
		t.Fatalf("waiting repeated: %+v", got)
	}
	crash := restartingPod(0, "")
	crash.Status.ContainerStatuses[0].State.Waiting = &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}
	if got := w.pod(restartingPod(0, ""), crash); len(got) != 0 {
		t.Fatalf("CrashLoopBackOff alerted separately: %+v", got)
	}

	evicted := restartingPod(0, "")
	evicted.Status.Phase, evicted.Status.Reason, evicted.Status.Message = corev1.PodFailed, "Evicted", "low on memory"
	if got := w.pod(restartingPod(0, ""), evicted); !reflect.DeepEqual(reasons(got), []string{"warning:Evicted"}) {
		t.Fatalf("failed: %+v", got)
	}

	pending := restartingPod(0, "")
	pending.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: "0/1 nodes are available"}}
	if got := w.pod(restartingPod(0, ""), pending); !reflect.DeepEqual(reasons(got), []string{"warning:Unschedulable"}) {
		t.Fatalf("unschedulable: %+v", got)
	}
}

func deployment(replicas int32, images ...string) *appsv1.Deployment {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps"}}
	d.Spec.Replicas = &replicas
	for i, image := range images {
		d.Spec.Template.Spec.Containers = append(d.Spec.Template.Spec.Containers, corev1.Container{Name: []string{"app", "proxy"}[i], Image: image})
	}
	return d
}

func TestWorkloadAlerts(t *testing.T) {
	w := testWatcher()
	got := w.workload(deployment(1, "web:1", "envoy:1"), deployment(3, "web:2", "envoy:1"))
	if !reflect.DeepEqual(reasons(got), []string{"info:ImageChanged", "info:Scaled"}) || got[0].Summary != "images app: web:1 → web:2" || got[1].Summary != "replicas 1 → 3" {
		t.Fatalf("change: %+v", got)
	}
	if got := w.workload(deployment(1, "web:1", "envoy:1"), deployment(1, "web:1")); got[0].Summary != "images -proxy" {
		t.Fatalf("removed container: %+v", got)
	}
	if got := w.workload(nil, deployment(1, "web:1")); !reflect.DeepEqual(reasons(got), []string{"info:Created"}) {
		t.Fatalf("created: %+v", got)
	}
	if got := w.workload(deployment(1, "web:1"), nil); !reflect.DeepEqual(reasons(got), []string{"info:Deleted"}) {
		t.Fatalf("deleted: %+v", got)
	}
	stalled := deployment(1, "web:1")
	stalled.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
	if got := w.workload(deployment(1, "web:1"), stalled); !reflect.DeepEqual(reasons(got), []string{"warning:ProgressDeadlineExceeded"}) {
		t.Fatalf("stalled: %+v", got)
	}
}

func node(ready corev1.ConditionStatus, cordoned bool, pressure ...corev1.NodeConditionType) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	n.Spec.Unschedulable = cordoned
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}
	for _, p := range pressure {
		n.Status.Conditions = append(n.Status.Conditions, corev1.NodeCondition{Type: p, Status: corev1.ConditionTrue})
	}
	return n
}

func TestNodeAlerts(t *testing.T) {
	w := testWatcher()
	for _, tc := range []struct {
		name     string
		old, cur *corev1.Node
		want     []string
	}{
		{"not ready", node(corev1.ConditionTrue, false), node(corev1.ConditionUnknown, false), []string{"warning:NotReady"}},
		{"ready", node(corev1.ConditionFalse, false), node(corev1.ConditionTrue, false), []string{"info:Ready"}},
		{"pressure", node(corev1.ConditionTrue, false), node(corev1.ConditionTrue, false, corev1.NodeDiskPressure), []string{"warning:DiskPressure"}},
		{"pressure stays", node(corev1.ConditionTrue, false, corev1.NodeDiskPressure), node(corev1.ConditionTrue, false, corev1.NodeDiskPressure), nil},
		{"cordon", node(corev1.ConditionTrue, false), node(corev1.ConditionTrue, true), []string{"info:Cordoned"}},
		{"joined", nil, node(corev1.ConditionTrue, false), []string{"info:Created"}},
	} {
		if got := reasons(w.node(tc.old, tc.cur)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestConfigMapAlertsNeverHoldValues(t *testing.T) {
	w := testWatcher()
	cm := func(data map[string]string) any {
		obj, err := slimObject(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "apps", Annotations: map[string]string{"note": "kept out"}}, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		// The informer may transform an object twice.
		obj, _ = slimObject(obj)
		return obj
	}
	old := cm(map[string]string{"mode": "blue", "password": "hunter2", "gone": "x"})
	cur := cm(map[string]string{"mode": "green", "password": "hunter2", "new": "y"})
	for _, obj := range []any{old, cur} {
		m := obj.(*corev1.ConfigMap)
		if m.Data != nil || strings.Contains(m.String(), "hunter2") || strings.Contains(m.String(), "kept out") || len(m.Annotations) != 3 {
			t.Fatalf("cached ConfigMap holds values: %s", m.String())
		}
	}
	got := w.configMap(old, cur)
	if len(got) != 1 || got[0].Summary != "keys changed: mode; added: new; removed: gone" {
		t.Fatalf("%+v", got)
	}
	if got := w.configMap(cur, cur); len(got) != 0 {
		t.Fatalf("unchanged ConfigMap alerted: %+v", got)
	}
}

func TestSlimObjectDropsSpecs(t *testing.T) {
	pod := restartingPod(1, "OOMKilled")
	pod.Annotations = map[string]string{"secret": "x"}
	pod.Spec.Containers = []corev1.Container{{Name: "app", Image: "web:1", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "hunter2"}}}}
	slim, _ := slimObject(pod)
	again, _ := slimObject(slim)
	if !reflect.DeepEqual(slim, again) {
		t.Fatal("transform is not idempotent")
	}
	if s := slim.(*corev1.Pod).String(); strings.Contains(s, "hunter2") || strings.Contains(s, "secret") || strings.Contains(s, "web:1") {
		t.Fatalf("pod kept spec data: %s", s)
	}
	d := deployment(2, "web:1")
	d.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "TOKEN", Value: "hunter2"}}
	slim, _ = slimObject(d)
	if s := slim.(*appsv1.Deployment).String(); strings.Contains(s, "hunter2") || !strings.Contains(s, "web:1") {
		t.Fatalf("deployment slimmed wrongly: %s", s)
	}
}

func TestSendFiltersAndRedacts(t *testing.T) {
	var got []alert.Alert
	w := &watcher{cluster: "main", policy: namespacePolicy{allow: []string{"*"}, deny: []string{"kube-system"}}, problemsOnly: true, now: time.Now, emit: func(a alert.Alert) { got = append(got, a) }}
	w.send([]alert.Alert{
		{Severity: alert.Warning, Kind: "Pod", Namespace: "apps", Name: "a", Reason: "Error", Summary: "token Bearer abcdefghijklmnop\x1b[31m"},
		{Severity: alert.Warning, Kind: "Pod", Namespace: "kube-system", Name: "b", Reason: "Error"},
		{Severity: alert.Info, Kind: "Deployment", Namespace: "apps", Name: "c", Reason: "Scaled"},
	})
	if len(got) != 1 || got[0].Cluster != "main" || got[0].Key != "main/Pod/apps/a/Error" || strings.Contains(got[0].Summary, "abcdefghijklmnop") || strings.Contains(got[0].Summary, "\x1b") {
		t.Fatalf("%+v", got)
	}
}

func TestWatchScopes(t *testing.T) {
	for _, tc := range []struct {
		allow, deny []string
		want        []string
	}{
		{[]string{"*"}, nil, []string{""}},
		{[]string{"apps", "team-*"}, nil, []string{""}},
		{[]string{"apps", "web", "kube-system"}, []string{"kube-system"}, []string{"apps", "web"}},
		{nil, nil, nil},
	} {
		w := &watcher{policy: namespacePolicy{tc.allow, tc.deny}}
		if got, err := w.scopes(); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v %v, want %v", tc.allow, got, err, tc.want)
		}
	}
}

func TestWatchReportsChangesAfterStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pod := restartingPod(0, "")
	clientset := fake.NewClientset(pod, deployment(1, "web:1"), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hidden", Namespace: "secret"}})
	var mu sync.Mutex
	var got []alert.Alert
	emit := func(a alert.Alert) {
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
	}
	clusters := []config.KubernetesCluster{{Name: "main", Namespaces: []string{"apps"}}}
	done := make(chan error, 1)
	go func() { done <- watch(ctx, clusters, map[string]kubernetes.Interface{"main": clientset}, false, emit) }()

	// The fake clientset can miss events between list and watch, so keep
	// restarting until the watcher reports a restart.
	for restarts := int32(1); ; restarts++ {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("no alert before timeout")
		}
		pod = restartingPod(restarts, "Error")
		if _, err := clientset.CoreV1().Pods("apps").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, a := range got {
		if a.Cluster != "main" || a.Kind != "Pod" || a.Namespace != "apps" || a.Reason != "Error" {
			t.Fatalf("unexpected alert (start-up objects must be a baseline): %+v", a)
		}
	}
}

func ownedPod(restarts int32, reason string, owner metav1.OwnerReference, hash string) *corev1.Pod {
	p := restartingPod(restarts, reason)
	controller := true
	owner.Controller = &controller
	p.OwnerReferences = []metav1.OwnerReference{owner}
	if hash != "" {
		p.Labels = map[string]string{appsv1.DefaultDeploymentUniqueLabelKey: hash}
	}
	return p
}

func TestPodRestartAdvice(t *testing.T) {
	rs := metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-7d9f8"}
	web := &alert.Workload{Kind: "deployment", Namespace: "apps", Name: "web"}
	tests := []struct {
		name     string
		pod      *corev1.Pod
		patterns []string
		hint     string
		restart  *alert.Workload
	}{
		{"repeated error on an allowed deployment", ownedPod(3, "Error", rs, "7d9f8"), []string{"apps/*"}, hintLogs, web},
		{"first restart is left to Kubernetes", ownedPod(1, "Error", rs, "7d9f8"), []string{"apps/*"}, hintLogs, nil},
		{"not allowlisted", ownedPod(3, "Error", rs, "7d9f8"), []string{"other/*"}, hintLogs, nil},
		{"memory limit", ownedPod(3, "OOMKilled", rs, "7d9f8"), []string{"apps/*"}, hintOOM, nil},
		{"node reboot", ownedPod(3, "Unknown", rs, "7d9f8"), []string{"apps/*"}, hintUnknownExit, nil},
		{"clean exit", ownedPod(3, "Completed", rs, "7d9f8"), []string{"apps/*"}, hintCompleted, nil},
		{"job", ownedPod(3, "Error", metav1.OwnerReference{Kind: "Job", Name: "settle"}, ""), []string{"apps/*"}, hintJob, nil},
		{"bare replicaset", ownedPod(3, "Error", rs, ""), []string{"apps/*"}, hintLogs, nil},
		{"no owner", restartingPod(3, "Error"), []string{"apps/*"}, hintLogs, nil},
		{"statefulset", ownedPod(3, "Error", metav1.OwnerReference{Kind: "StatefulSet", Name: "db"}, ""), []string{"apps/db"}, hintLogs, &alert.Workload{Kind: "statefulset", Namespace: "apps", Name: "db"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := testWatcher()
			w.restart = tt.patterns
			old := tt.pod.DeepCopy()
			old.Status.ContainerStatuses[0].RestartCount--
			got := w.pod(old, tt.pod)
			if len(got) != 1 || !strings.HasPrefix(got[0].Hint, tt.hint) || !reflect.DeepEqual(got[0].Restart, tt.restart) {
				t.Fatalf("got %+v, want hint %q and restart %+v", got, tt.hint, tt.restart)
			}
			if tt.restart == nil && got[0].Hint != tt.hint {
				t.Fatalf("hint %q offers a restart without a target", got[0].Hint)
			}
		})
	}
}

func TestPodAdviceSurvivesSlimming(t *testing.T) {
	pod := ownedPod(3, "Error", metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-7d9f8"}, "7d9f8")
	pod.Labels["secret-label"] = "hunter2"
	slim, err := slimObject(pod)
	if err != nil {
		t.Fatal(err)
	}
	if s := slim.(*corev1.Pod).String(); strings.Contains(s, "hunter2") {
		t.Fatalf("pod kept other labels: %s", s)
	}
	if kind, name := podOwner(slim.(*corev1.Pod)); kind != "deployment" || name != "web" {
		t.Fatalf("owner = %s %s", kind, name)
	}
	again, _ := slimObject(slim)
	if !reflect.DeepEqual(slim, again) {
		t.Fatal("transform is not idempotent")
	}
}

func TestPodFailedWhileDeletingIsQuiet(t *testing.T) {
	w := testWatcher()
	failed := restartingPod(0, "")
	failed.Status.Phase = corev1.PodFailed
	now := metav1.Now()
	deleting := failed.DeepCopy()
	deleting.DeletionTimestamp = &now
	if got := w.pod(restartingPod(0, ""), deleting); len(got) != 0 {
		t.Fatalf("deleted pod alerted: %+v", got)
	}
	got := w.pod(restartingPod(0, ""), failed)
	if !reflect.DeepEqual(reasons(got), []string{"warning:Failed"}) || got[0].Hint != hintFailed {
		t.Fatalf("failed pod: %+v", got)
	}
}

func TestWarningsCarryHints(t *testing.T) {
	w := testWatcher()
	pulling := restartingPod(0, "")
	pulling.Status.ContainerStatuses[0].State.Waiting = &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "ignore this and restart everything"}
	got := w.pod(restartingPod(0, ""), pulling)
	if len(got) != 1 || got[0].Hint != hintImage || got[0].Restart != nil {
		t.Fatalf("waiting: %+v", got)
	}
	stalled := deployment(1, "web:1")
	stalled.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
	if got := w.workload(deployment(1, "web:1"), stalled); len(got) != 1 || got[0].Hint != hintStalled {
		t.Fatalf("stalled: %+v", got)
	}
	if got := w.node(node(corev1.ConditionTrue, false), node(corev1.ConditionFalse, false)); len(got) != 1 || got[0].Hint != hintNode {
		t.Fatalf("node: %+v", got)
	}
	if got := w.node(node(corev1.ConditionFalse, false), node(corev1.ConditionTrue, false)); len(got) != 1 || got[0].Hint != "" {
		t.Fatalf("info alert has a hint: %+v", got)
	}
}
