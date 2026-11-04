package kube

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

func TestListPolicyAndValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		in     listInput
		want   string
		denied bool
	}{
		{"filter", listInput{Kind: "po"}, "visible", false},
		{"deny", listInput{Kind: "pods", Namespace: "private"}, "", true},
		{"invalid namespace", listInput{Kind: "pods", Namespace: "../x"}, "invalid name", false},
		{"selector", listInput{Kind: "po", LabelSelector: "x in ("}, "invalid label_selector", false},
		{"invalid sensitive selector", listInput{Kind: "po", LabelSelector: "password in ("}, "invalid label_selector", false},
		{"secret", listInput{Kind: "secrets"}, "supported kinds:", false},
		{"cluster namespace", listInput{Kind: "no", Namespace: "public"}, "cluster scoped", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "visible", Namespace: "public"}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hidden", Namespace: "private"}})
			text, err := p.list(context.Background(), tool.Caller{}, tt.in)
			if tt.denied {
				if !errors.Is(err, tool.ErrDenied) {
					t.Fatal(err)
				}
				if len(p.client.(*fake.Clientset).Actions()) != 0 {
					t.Fatal("denied request reached cluster")
				}
				return
			}
			if err != nil {
				text = err.Error()
			}
			if !strings.Contains(text, tt.want) || strings.Contains(text, "hidden") {
				t.Fatal(text, err)
			}
		})
	}
}

func TestListSensitiveSelectors(t *testing.T) {
	for _, key := range []string{"password", "DB_PASSWORD", "dbpassword", "example.com/token", "session_id", "COOKIE"} {
		for _, selector := range []string{
			key + "=hunter2",
			key + "=wrongguess",
			key + "==hunter2",
			key + "!=hunter2",
			key + " in (hunter2,wrongguess)",
			key + " notin (hunter2,wrongguess)",
			key,
			"!" + key,
			"app=web," + key + "=hunter2",
		} {
			t.Run(selector, func(t *testing.T) {
				p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "visible", Namespace: "public", Labels: map[string]string{key: "hunter2", "app": "web"}}})
				out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pods", LabelSelector: selector})
				if !errors.Is(err, tool.ErrDenied) || out != "" {
					t.Fatalf("selector was not denied: %q, %v", out, err)
				}
				if !strings.Contains(err.Error(), "sensitive key") || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "wrongguess") {
					t.Fatalf("denial must name the key rule without values: %v", err)
				}
				if len(p.client.(*fake.Clientset).Actions()) != 0 {
					t.Fatal("denied selector reached cluster")
				}
			})
		}
	}
}

func TestListOrdinarySelectors(t *testing.T) {
	for _, key := range []string{"app", "password_file", "monkey"} {
		for _, tc := range []struct {
			selector string
			match    bool
		}{
			{"", true},
			{key + "=web", true},
			{key + "==web", true},
			{key + "=other", false},
			{key + "!=web", false},
			{key + " in (web,other)", true},
			{key + " notin (web,other)", false},
			{key, true},
			{"!" + key, false},
		} {
			t.Run(tc.selector, func(t *testing.T) {
				p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "visible", Namespace: "public", Labels: map[string]string{key: "web"}}})
				client := p.client.(*fake.Clientset)
				client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
					if got := action.(ktesting.ListActionImpl).GetListOptions().LabelSelector; got != tc.selector {
						t.Errorf("selector changed to %q", got)
					}
					return false, nil, nil
				})
				out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pods", LabelSelector: tc.selector})
				if err != nil || strings.Contains(out, "visible") != tc.match {
					t.Fatal(out, err)
				}
				if len(client.Actions()) != 1 {
					t.Fatal(client.Actions())
				}
			})
		}
	}
}

func TestListTruncation(t *testing.T) {
	var objects []runtime.Object
	for i := 0; i < 3; i++ {
		objects = append(objects, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d", i), Namespace: "public"}})
	}
	p := testProvider(objects...)
	out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pods", Limit: 1})
	if err != nil || !strings.Contains(out, "truncated to 1") || strings.Contains(out, "pod-1") {
		t.Fatal(out, err)
	}
}

func TestListCollectionBudget(t *testing.T) {
	for _, limit := range []int{1, 500} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			p := testProvider()
			calls := 0
			p.client.(*fake.Clientset).PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				calls++
				opts := action.(ktesting.ListActionImpl).GetListOptions()
				pageSize := min(limit+1, 500)
				if opts.Limit != int64(pageSize) || opts.LabelSelector != "app=web" {
					t.Errorf("list options = %+v", opts)
				}
				if calls > 3 {
					return true, nil, errors.New("read beyond authorized result budget")
				}
				page := &corev1.PodList{ListMeta: metav1.ListMeta{Continue: fmt.Sprint(calls)}}
				for i := 0; i < pageSize; i++ {
					ns := "public"
					if calls == 1 {
						ns = "private" // Denied items must not spend the budget.
					}
					page.Items = append(page.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d-%03d", calls, i), Namespace: ns, Labels: map[string]string{"app": "web"}}})
				}
				return true, page, nil
			})
			out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pods", Limit: limit, LabelSelector: "app=web"})
			wantCalls := 2
			if limit == 500 {
				wantCalls = 3
			}
			if err != nil || calls != wantCalls || !strings.Contains(out, fmt.Sprintf("truncated to %d objects", limit)) || strings.Contains(out, "private") {
				t.Fatalf("calls=%d: %s, %v", calls, out, err)
			}
		})
	}
}

func TestCollectRetainsTableFieldsOnly(t *testing.T) {
	p := testProvider(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "public", Annotations: map[string]string{"large": strings.Repeat("x", 1<<20)}}, Data: map[string]string{"one": "secret-payload"}, BinaryData: map[string][]byte{"two": []byte("binary-payload")}})
	items, err := p.collect(context.Background(), resource{"configmaps", "ConfigMap", false}, "public", "")
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	text := fmt.Sprint(items)
	if strings.Contains(text, "payload") || strings.Contains(text, "annotations") || tableCells(items[0], "configmaps")[0] != "2" {
		t.Fatalf("retained non-table data (%d bytes)", len(text))
	}
}

func TestSanitizeContainerFlags(t *testing.T) {
	for _, kind := range []string{"pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs"} {
		t.Run(kind, func(t *testing.T) {
			spec := map[string]any{}
			for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
				container := map[string]any{}
				for _, argv := range []string{"command", "args"} {
					container[argv] = []any{"app", "--password", "flag-password\n", "--token", "flag-token", "--secret", "flag-secret", "--api-key", "flag-api-key", "--client-secret", "flag-client-secret", "--db-password", "flag-db-password", "-p", "keep-short", "--port", "8080"}
				}
				spec[field] = []any{container}
			}
			m := map[string]any{"spec": spec}
			if kind == "cronjobs" {
				m["spec"] = map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": spec}}}}
			} else if kind != "pods" {
				m["spec"] = map[string]any{"template": map[string]any{"spec": spec}}
			}
			sanitize(m, kind, false)
			data, err := yaml.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			text := redact.String(string(data))
			if strings.Contains(text, "flag-") || strings.Count(text, redact.Mask) != 36 || !strings.Contains(text, "keep-short") || !strings.Contains(text, "8080") {
				t.Fatal(text)
			}
		})
	}
}

func TestGetRedactsStructuredValues(t *testing.T) {
	p := testProvider(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "public", Annotations: map[string]string{"password": "multiline-secret\n", "client_secret": "quoted-secret\n"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Args: []string{"--password", "argument-secret\n", "-p", "keep-short"}}}}})
	out, err := p.get(context.Background(), tool.Caller{}, getInput{Kind: "pods", Namespace: "public", Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	out = redact.String(out)
	for _, secret := range []string{"multiline-secret", "quoted-secret", "argument-secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("leaked %s: %s", secret, out)
		}
	}
	if !strings.Contains(out, "keep-short") || strings.Count(out, redact.Mask) != 3 {
		t.Fatal(out)
	}
}

func TestSanitize(t *testing.T) {
	for _, kind := range []string{"pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs"} {
		t.Run(kind, func(t *testing.T) {
			spec := map[string]any{}
			for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
				spec[field] = []any{map[string]any{"name": "app", "env": []any{map[string]any{"name": "DATABASE_URL", "value": "topsecret"}, map[string]any{"name": "NORMAL", "value": "keep"}, map[string]any{"name": "TOKEN", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "reference", "key": "token"}}}}}}
			}
			m := map[string]any{"metadata": map[string]any{"managedFields": []any{"private"}, "annotations": map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "topsecret"}}}
			switch kind {
			case "pods":
				m["spec"] = spec
			case "cronjobs":
				m["spec"] = map[string]any{"jobTemplate": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": spec}}}}
			default:
				m["spec"] = map[string]any{"template": map[string]any{"spec": spec}}
			}
			sanitize(m, kind, false)
			text := fmt.Sprint(m)
			for _, bad := range []string{"topsecret", "managedFields", "last-applied-configuration"} {
				if strings.Contains(text, bad) {
					t.Fatal(text)
				}
			}
			for _, good := range []string{"[REDACTED]", "keep", "reference"} {
				if !strings.Contains(text, good) {
					t.Fatal(text)
				}
			}
		})
	}
	for _, show := range []bool{true, false} {
		m := map[string]any{"data": map[string]any{"a": "é"}, "binaryData": map[string]any{"b": "YWJj"}}
		sanitize(m, "configmaps", show)
		s := fmt.Sprint(m)
		if !show && (!strings.Contains(s, "<2 bytes hidden>") || !strings.Contains(s, "<3 bytes hidden>")) {
			t.Fatal(s)
		}
		if show && !strings.Contains(s, "YWJj") {
			t.Fatal(s)
		}
	}
}

func TestGet(t *testing.T) {
	for _, tt := range []struct {
		in   getInput
		want string
	}{
		{getInput{Kind: "cm", Namespace: "public", Name: "settings"}, "<6 bytes hidden>"},
		{getInput{Kind: "cm", Name: "settings"}, "namespace is required"},
		{getInput{Kind: "cm", Namespace: "private", Name: "settings"}, "denied by policy"},
		{getInput{Kind: "cm", Namespace: "public", Name: "../bad"}, "invalid name"},
	} {
		p := testProvider(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "public"}, Data: map[string]string{"password": "secret", "normal": "public"}})
		out, err := p.get(context.Background(), tool.Caller{}, tt.in)
		if err != nil {
			out = err.Error()
		}
		if !strings.Contains(out, tt.want) || strings.Contains(out, "password: secret") {
			t.Fatal(out)
		}
	}
}

func TestPodProblems(t *testing.T) {
	for _, tt := range []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{"not ready", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, "not Ready"},
		{"pending", corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute))}, Status: corev1.PodStatus{Phase: corev1.PodPending}}, "Pending > 5m"},
		{"waiting", corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", RestartCount: 5, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}}}, "CrashLoopBackOff"},
		{"total restarts", corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "one", RestartCount: 3}, {Name: "two", RestartCount: 3}}}}, "6 total restarts"},
		{"healthy", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, err := objectMap(&tt.pod)
			if err != nil {
				t.Fatal(err)
			}
			got := podProblem(m, time.Now())
			if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
				t.Fatal(got)
			}
		})
	}
}

func TestOverview(t *testing.T) {
	replicas := int32(3)
	p := testProvider(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "public"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1}}, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hidden", Namespace: "private"}})
	out, err := p.overview(context.Background(), tool.Caller{}, struct{}{})
	if err != nil || !strings.Contains(out, "1/3") || strings.Contains(out, "hidden") {
		t.Fatal(out, err)
	}
}

func TestEveryResource(t *testing.T) {
	for _, kind := range []string{"pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs", "services", "ingresses", "configmaps", "persistentvolumeclaims", "persistentvolumes", "nodes", "namespaces"} {
		t.Run(kind, func(t *testing.T) {
			p := testProvider()
			out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: kind})
			if err != nil || !strings.Contains(out, "NAME") {
				t.Fatal(out, err)
			}
			r, err := resolveKind(kind)
			if err != nil {
				t.Fatal(err)
			}
			ns := "public"
			if r.cluster {
				ns = ""
			}
			_, err = p.get(context.Background(), tool.Caller{}, getInput{Kind: kind, Namespace: ns, Name: "missing"})
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatal(err)
			}
		})
	}
	for alias, want := range map[string]string{"po": "pods", "deploy": "deployments", "sts": "statefulsets", "ds": "daemonsets", "rs": "replicasets", "svc": "services", "ing": "ingresses", "cm": "configmaps", "pvc": "persistentvolumeclaims", "pv": "persistentvolumes", "no": "nodes", "ns": "namespaces"} {
		r, err := resolveKind(alias)
		if err != nil || r.name != want {
			t.Fatal(alias, r, err)
		}
	}
}

func TestPersistentVolumeClaimPolicy(t *testing.T) {
	for _, policy := range []namespacePolicy{
		{allow: []string{"public"}},
		{allow: []string{"*"}, deny: []string{"private"}},
	} {
		var objects []runtime.Object
		for _, tt := range []struct{ name, ns string }{
			{"allowed", "public"}, {"hidden", "private"}, {"empty-claim", ""}, {"unbound", ""},
		} {
			pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: tt.name}}
			if tt.name != "unbound" {
				pv.Spec.ClaimRef = &corev1.ObjectReference{Namespace: tt.ns, Name: "claim"}
			}
			objects = append(objects, pv)
		}
		p := testProvider(objects...)
		p.policy = policy
		out, err := p.list(context.Background(), tool.Caller{}, listInput{Kind: "pv"})
		if err != nil || strings.Contains(out, "hidden") || strings.Contains(out, "private") {
			t.Fatal(out, err)
		}
		for _, name := range []string{"allowed", "empty-claim", "unbound"} {
			if !strings.Contains(out, name) {
				t.Fatal(out)
			}
			got, err := p.get(context.Background(), tool.Caller{}, getInput{Kind: "pv", Name: name})
			if err != nil || !strings.Contains(got, "kind: PersistentVolume") {
				t.Fatal(got, err)
			}
		}
		before := len(p.client.(*fake.Clientset).Actions())
		out, err = p.get(context.Background(), tool.Caller{}, getInput{Kind: "pv", Name: "hidden"})
		if out != "" || !errors.Is(err, tool.ErrDenied) || err.Error() != tool.Denied("namespace is not allowed").Error() {
			t.Fatal(out, err)
		}
		if actions := p.client.(*fake.Clientset).Actions()[before:]; len(actions) != 1 || actions[0].GetResource().Resource != "persistentvolumes" {
			t.Fatal("denied PV queried events", actions)
		}
	}
}
