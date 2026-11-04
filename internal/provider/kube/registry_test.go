package kube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func clusterFixture() (config.Kubernetes, map[string]kubernetes.Interface) {
	cfg := config.Kubernetes{Clusters: []config.KubernetesCluster{
		{Name: "kind", Namespaces: []string{"apps"}, AllowLogs: true, RolloutRestart: []string{"apps/*"}, Scale: []string{"apps/*"}, DeletePod: []string{"apps/*"}},
		{Name: "prod", Protected: true, Namespaces: []string{"apps"}, DenyNamespaces: []string{"private"}},
	}}
	clients := map[string]kubernetes.Interface{}
	for _, cluster := range cfg.Clusters {
		md := metav1.ObjectMeta{Name: "web", Namespace: "apps", UID: types.UID("uid-" + cluster.Name), ResourceVersion: "1", Labels: map[string]string{"cluster": cluster.Name}}
		clients[cluster.Name] = fake.NewClientset(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}},
			&corev1.Pod{ObjectMeta: md, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}},
			&appsv1.Deployment{ObjectMeta: md, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}}}},
			&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "event", Namespace: "apps"}, Type: "Warning", Message: "event-" + cluster.Name, LastTimestamp: metav1.Now(), InvolvedObject: corev1.ObjectReference{Namespace: "apps", Kind: "Pod", Name: "web"}},
		)
	}
	return cfg, clients
}

func clusterSession(t *testing.T, cfg config.Kubernetes, clients map[string]kubernetes.Interface, confirm tool.Confirmer) *mcp.ClientSession {
	t.Helper()
	logger, err := audit.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	env := &tool.Env{Audit: logger, Timeout: 5 * time.Second, MaxOutputBytes: 65536, Confirm: confirm, Fallback: &tool.Caller{User: "test", Scopes: config.Scopes}}
	if err := register(s, env, cfg, clients); err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if len(client.(*fake.Clientset).Actions()) > 0 {
			t.Fatal("registration contacted a cluster")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	a, b := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestClusterReads(t *testing.T) {
	for _, name := range []string{"kind", "prod"} {
		for _, tc := range []struct {
			tool string
			args map[string]any
			want string
		}{
			{"k8s_list", map[string]any{"kind": "pods", "namespace": "apps"}, "web"},
			{"k8s_get", map[string]any{"kind": "pod", "namespace": "apps", "name": "web"}, "uid-" + name},
			{"k8s_events", map[string]any{"namespace": "apps"}, "event-" + name},
			{"k8s_logs", map[string]any{"namespace": "apps", "pod": "web"}, "fake logs"},
			{"k8s_overview", map[string]any{}, "POD COUNTS"},
		} {
			t.Run(name+"/"+tc.tool, func(t *testing.T) {
				cfg, clients := clusterFixture()
				cfg.Clusters[1].AllowLogs = true
				cs := clusterSession(t, cfg, clients, nil)
				tc.args["cluster"] = name
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
				if err != nil {
					t.Fatal(err)
				}
				out := result.Content[0].(*mcp.TextContent).Text
				if result.IsError || !strings.HasPrefix(out, "cluster: "+name+"\n") || !strings.Contains(out, tc.want) {
					t.Fatal(out)
				}
				for cluster, client := range clients {
					actions := client.(*fake.Clientset).Actions()
					if (len(actions) > 0) != (cluster == name) {
						t.Fatalf("%s actions: %v", cluster, actions)
					}
					if cluster == name && tc.tool == "k8s_logs" {
						found := false
						for _, a := range actions {
							found = found || a.GetSubresource() == "log"
						}
						if !found {
							t.Fatal("log client was not called")
						}
					}
				}
			})
		}
	}
}

func TestClusterResolution(t *testing.T) {
	for _, tc := range []struct {
		name, cluster, want string
		single              bool
	}{
		{"missing", "", "cluster is required; configured clusters: kind, prod", false},
		{"unknown", "other", "unknown cluster; configured clusters: kind, prod", false},
		{"single omitted", "", "", true},
		{"single named", "kind", "", true},
		{"single unknown", "prod", "unknown cluster; configured clusters: kind", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, clients := clusterFixture()
			if tc.single {
				cfg.Clusters = cfg.Clusters[:1]
				delete(clients, "prod")
			}
			r := &registry{byName: map[string]*provider{}}
			for _, c := range cfg.ClusterList() {
				p := &provider{cfg: c, client: clients[c.Name], policy: namespacePolicy{c.Namespaces, c.DenyNamespaces}}
				r.clusters = append(r.clusters, p)
				r.byName[c.Name] = p
			}
			out, err := clusterRead(r, (*provider).list)(context.Background(), tool.Caller{}, listInput{Cluster: tc.cluster, Kind: "pod", Namespace: "apps"})
			if tc.want != "" {
				if !errors.Is(err, tool.ErrDenied) || !strings.Contains(err.Error(), tc.want) {
					t.Fatal(err)
				}
				for _, c := range clients {
					if len(c.(*fake.Clientset).Actions()) > 0 {
						t.Fatal("denial contacted API")
					}
				}
			} else if err != nil || !strings.HasPrefix(out, "cluster: kind\n") {
				t.Fatal(out, err)
			}
		})
	}
}

func TestClusterDescriptionsAndGates(t *testing.T) {
	cfg, clients := clusterFixture()
	cs := clusterSession(t, cfg, clients, nil)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range list.Tools {
		if spec.Name != "k8s_clusters" && !strings.Contains(spec.Description, "Configured clusters: kind, prod; cluster is required.") {
			t.Fatalf("%s: %s", spec.Name, spec.Description)
		}
	}
	for _, name := range []string{"k8s_logs", "k8s_scale", "k8s_rollout_restart", "k8s_delete_pod"} {
		args := map[string]any{"cluster": "prod", "namespace": "apps", "name": "web"}
		if name == "k8s_logs" {
			args = map[string]any{"cluster": "prod", "namespace": "apps", "pod": "web"}
		}
		if name == "k8s_scale" || name == "k8s_rollout_restart" {
			args["kind"] = "deployment"
		}
		if name == "k8s_scale" {
			args["replicas"] = 2
		}
		result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !result.IsError {
			t.Fatal(result, err)
		}
		if out := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(out, name+" is disabled on cluster prod") {
			t.Fatal(out)
		}
	}
	if len(clients["prod"].(*fake.Clientset).Actions()) != 0 {
		t.Fatal("disabled tools contacted API")
	}
}

type captureConfirmation struct{ request tool.ConfirmRequest }

func (c *captureConfirmation) Confirm(_ context.Context, _ *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	c.request = r
	return "test", nil
}

func TestClusterWrites(t *testing.T) {
	for _, protected := range []bool{false, true} {
		for _, name := range []string{"k8s_rollout_restart", "k8s_scale", "k8s_delete_pod"} {
			t.Run(name+map[bool]string{false: "/ordinary", true: "/protected"}[protected], func(t *testing.T) {
				cfg, clients := clusterFixture()
				cfg.Clusters[0].Protected = protected
				if name == "k8s_scale" {
					client := clients["kind"].(*fake.Clientset)
					client.PrependReactor("get", "deployments", func(a kt.Action) (bool, runtime.Object, error) {
						if a.GetSubresource() != "scale" {
							return false, nil, nil
						}
						return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps", UID: "uid-kind", ResourceVersion: "1"}}, nil
					})
					client.PrependReactor("update", "deployments", func(a kt.Action) (bool, runtime.Object, error) {
						if a.GetSubresource() != "scale" {
							return false, nil, nil
						}
						return true, a.(kt.UpdateAction).GetObject(), nil
					})
				}
				confirm := &captureConfirmation{}
				cs := clusterSession(t, cfg, clients, confirm)
				args := map[string]any{"cluster": "kind", "namespace": "apps", "name": "web"}
				if name != "k8s_delete_pod" {
					args["kind"] = "deployment"
				}
				if name == "k8s_scale" {
					args["replicas"] = 2
				}
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				out := result.Content[0].(*mcp.TextContent).Text
				if result.IsError || !strings.HasPrefix(out, "cluster: kind\n") {
					t.Fatal(out)
				}
				r := confirm.request
				if !strings.HasPrefix(r.Summary, `Cluster "kind": `) || r.Target != `Kubernetes cluster "kind"` || r.NoWindow != protected {
					t.Fatalf("confirmation = %+v", r)
				}
				if len(clients["prod"].(*fake.Clientset).Actions()) != 0 {
					t.Fatal("write reached prod")
				}
			})
		}
	}
}

func TestEveryToolRequiresCluster(t *testing.T) {
	for _, cluster := range []string{"", "unknown"} {
		for _, tc := range []struct {
			name string
			args map[string]any
		}{
			{"k8s_overview", map[string]any{}},
			{"k8s_list", map[string]any{"kind": "pods"}},
			{"k8s_get", map[string]any{"kind": "pods", "namespace": "apps", "name": "web"}},
			{"k8s_events", map[string]any{}},
			{"k8s_logs", map[string]any{"namespace": "apps", "pod": "web"}},
			{"k8s_rollout_restart", map[string]any{"kind": "deployment", "namespace": "apps", "name": "web"}},
			{"k8s_scale", map[string]any{"kind": "deployment", "namespace": "apps", "name": "web", "replicas": 2}},
			{"k8s_delete_pod", map[string]any{"namespace": "apps", "name": "web"}},
		} {
			t.Run(tc.name+"/"+cluster, func(t *testing.T) {
				cfg, clients := clusterFixture()
				cs := clusterSession(t, cfg, clients, nil)
				if cluster != "" {
					tc.args["cluster"] = cluster
				}
				result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
				if err != nil || !result.IsError {
					t.Fatal(result, err)
				}
				out := result.Content[0].(*mcp.TextContent).Text
				if !strings.Contains(out, "denied by policy") || !strings.Contains(out, "configured clusters: kind, prod") {
					t.Fatal(out)
				}
				for _, c := range clients {
					if len(c.(*fake.Clientset).Actions()) > 0 {
						t.Fatal("denial contacted API")
					}
				}
			})
		}
	}
}

func TestOnlyRolloutRestartIsDirect(t *testing.T) {
	cfg, clients := clusterFixture()
	env := &tool.Env{Timeout: 5 * time.Second, MaxOutputBytes: 65536}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	if err := register(s, env, cfg, clients); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"k8s_scale", "k8s_delete_pod"} {
		if env.Direct(name) != nil {
			t.Fatalf("%s can run without a client", name)
		}
	}
	d := env.Direct("k8s_rollout_restart")
	if d == nil {
		t.Fatal("k8s_rollout_restart is not direct")
	}
	ctx := context.Background()
	caller := tool.Caller{User: "a", Transport: "web", Scopes: config.Scopes}
	raw := json.RawMessage(`{"cluster":"kind","kind":"deployment","namespace":"apps","name":"web"}`)
	_, shown, err := d.Plan(ctx, caller, raw)
	if err != nil || !strings.Contains(shown, "Rollout restart deployment apps/web") {
		t.Fatalf("plan: %q %v", shown, err)
	}
	client := clients["kind"].(*fake.Clientset)
	if _, err := d.Run(ctx, caller, raw, shown); err != nil {
		t.Fatal(err)
	}
	dep, err := client.AppsV1().Deployments("apps").Get(ctx, "web", metav1.GetOptions{})
	if err != nil || dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Fatalf("restart not applied: %+v %v", dep.Spec.Template.Annotations, err)
	}
	for name, target := range map[string]string{
		"not allowlisted":      `{"cluster":"prod","kind":"deployment","namespace":"apps","name":"web"}`,
		"namespace not listed": `{"cluster":"kind","kind":"deployment","namespace":"other","name":"web"}`,
		"unsupported kind":     `{"cluster":"kind","kind":"pod","namespace":"apps","name":"web"}`,
	} {
		if _, _, err := d.Plan(ctx, caller, json.RawMessage(target)); err == nil {
			t.Fatalf("%s: plan succeeded", name)
		}
	}
}
