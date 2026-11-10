package kube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func TestActionLinkReadOnlyAndPolicy(t *testing.T) {
	cfg, clients := clusterFixture()
	cfg.Clusters[0].ActionApprovalURL = "https://ops.example"
	cfg.Clusters[0].RolloutRestart = []string{"apps/web", "apps/missing"}
	clients["kind"].(*fake.Clientset).PrependReactor("get", "deployments", func(a kt.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "apps", UID: "uid-kind", ResourceVersion: "1"}, Spec: autoscalingv1.ScaleSpec{Replicas: 1}}, nil
	})
	path := filepath.Join(t.TempDir(), "audit.log")
	logger, err := audit.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	caller := tool.Caller{User: "reader", Transport: "http", Scopes: []string{config.ScopeRead}}
	env := &tool.Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 65536, Fallback: &caller}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	if err := register(s, env, cfg, clients); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, b := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, tc := range []struct {
		name, target, namespace, extra string
		wantError                      bool
	}{
		{"success", "k8s_rollout_restart", "apps", "", false},
		{"scale", "k8s_scale", "apps", "", false},
		{"owned pod", "k8s_delete_pod", "apps", "owned", false},
		{"rule source", "k8s_rollout_restart", "apps", "rule", true},
		{"bad source", "k8s_rollout_restart", "apps", "bad source", true},
		{"unknown", "unknown", "apps", "", true},
		{"note", "note_trash", "apps", "", true},
		{"extra", "k8s_rollout_restart", "apps", "extra", true},
		{"approval id", "k8s_rollout_restart", "apps", "approval_id", true},
		{"source arg", "k8s_rollout_restart", "apps", "source", true},
		{"namespace", "k8s_rollout_restart", "private", "", true},
		{"disabled cluster", "k8s_rollout_restart", "apps", "cluster", true},
		{"disallowed target", "k8s_rollout_restart", "apps", "disallowed", true},
		{"missing target", "k8s_rollout_restart", "apps", "name", true},
		{"wrong type", "k8s_rollout_restart", "apps", "kind", true},
		{"masked", "k8s_rollout_restart", "apps", "masked", true},
		{"naked pod", "k8s_delete_pod", "apps", "", true},
		{"caller tool policy", "k8s_rollout_restart", "apps", "policy", true},
		{"revert suggestion", "k8s_restore_image", "apps", "revert", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"cluster": "kind", "namespace": tc.namespace, "name": "web"}
			if tc.target == "k8s_rollout_restart" || tc.target == "k8s_scale" {
				args["kind"] = "deployment"
			}
			if tc.target == "k8s_scale" {
				args["replicas"] = 2
			}
			caller.Tools = nil
			source := ""
			switch tc.extra {
			case "cluster":
				args["cluster"] = "prod"
			case "owned":
				controller := true
				pod, err := clients["kind"].CoreV1().Pods("apps").Get(ctx, "web", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs", UID: "rs-uid", Controller: &controller}}
				if _, err := clients["kind"].CoreV1().Pods("apps").Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				clients["kind"].(*fake.Clientset).ClearActions()
				t.Cleanup(func() {
					pod.OwnerReferences = nil
					_, _ = clients["kind"].CoreV1().Pods("apps").Update(ctx, pod, metav1.UpdateOptions{})
					clients["kind"].(*fake.Clientset).ClearActions()
				})
			case "revert":
				args["container"], args["expected_image"], args["image"], args["revert"] = "app", "app:2", "app:1", true
			case "rule":
				source = "rule"
			case "bad source":
				source = "invalid"
			case "disallowed":
				args["name"] = "other"
			case "name":
				args["name"] = "missing"
			case "kind":
				args["kind"] = 12
			case "masked":
				args["name"] = "ghp_12345678901234567890"
			case "policy":
				caller.Tools = []string{"action_link"}
			case "":
			default:
				args[tc.extra] = "forged"
			}
			out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "action_link", Arguments: map[string]any{"tool": tc.target, "arguments": args, "source": source}})
			if err != nil {
				t.Fatal(err)
			}
			if out.IsError != tc.wantError {
				t.Fatalf("error=%v: %v", out.IsError, out.Content)
			}
			if !tc.wantError {
				entry, _ := action.Lookup(tc.target)
				wantSource := "ai"
				var result map[string]string
				if json.Unmarshal([]byte(out.Content[0].(*mcp.TextContent).Text), &result) != nil || !strings.Contains(result["url"], "/approve/action/"+tc.target+"?") || !strings.Contains(result["url"], "source="+wantSource) || result["label"] != entry.Label("en") || !strings.Contains(result["summary"], "apps/web") {
					t.Fatalf("bad result: %v", result)
				}
			}
			for _, client := range clients {
				for _, call := range client.(*fake.Clientset).Actions() {
					if call.GetVerb() != "get" && call.GetVerb() != "list" {
						t.Fatalf("read tool wrote: %v", call)
					}
				}
			}
		})
	}
	caller.Tools = nil
	raw := json.RawMessage(`{"cluster":"kind","kind":"deployment","namespace":"apps","name":"web"}`)
	if _, err := env.Direct("k8s_rollout_restart").Run(ctx, caller, raw, "forged"); err == nil {
		t.Fatal("read scope executed direct action")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"tool":"action_link"`) || !strings.Contains(string(data), `"outcome":"ok"`) || !strings.Contains(string(data), `"outcome":"denied"`) {
		t.Fatalf("missing read audit: %s", data)
	}
}

func TestActionLinkRegistration(t *testing.T) {
	for _, scenario := range []string{"enabled", "no base", "no actions", "server policy"} {
		cfg, clients := clusterFixture()
		cfg.Clusters[0].ActionApprovalURL = "https://ops.example"
		env := &tool.Env{Timeout: time.Second}
		switch scenario {
		case "no base":
			cfg.Clusters[0].ActionApprovalURL = ""
		case "no actions":
			cfg.Clusters[0].RolloutRestart = nil
			cfg.Clusters[0].Scale = nil
			cfg.Clusters[0].DeletePod = nil
		case "server policy":
			env.Policy = tool.NewPolicy(config.ToolPolicy{Deny: []string{"k8s_rollout_restart", "k8s_scale", "k8s_delete_pod"}})
		}
		s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
		if err := register(s, env, cfg, clients); err != nil {
			t.Fatal(err)
		}
		a, b := mcp.NewInMemoryTransports()
		ctx := context.Background()
		ss, err := s.Connect(ctx, a, nil)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, b, nil)
		if err != nil {
			t.Fatal(err)
		}
		list, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range list.Tools {
			if item.Name == "action_link" {
				found = true
				if !item.Annotations.ReadOnlyHint {
					t.Fatal("not read only")
				}
			}
		}
		cs.Close()
		ss.Close()
		if found != (scenario == "enabled") {
			t.Fatalf("%s: registered=%v", scenario, found)
		}
	}
}

func TestDeletePodLinkRequiresController(t *testing.T) {
	controller := true
	for _, owned := range []bool{false, true} {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web", UID: "uid", ResourceVersion: "1"}}
		if owned {
			pod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs", UID: "rs-uid", Controller: &controller}}
		}
		p, _ := writeProvider(pod)
		in := deletePodInput{Namespace: "prod", Name: "web"}
		for _, transport := range []string{"web", "http", "stdio"} {
			plan, err := p.planDeletePod(context.Background(), tool.Caller{Transport: transport}, in)
			if (err != nil) != (!owned && transport == "web") {
				t.Fatalf("owned=%v transport=%s: %v", owned, transport, err)
			}
			if err == nil && !owned && !strings.Contains(plan.Summary, "will NOT be recreated") {
				t.Fatal("MCP warning lost")
			}
		}
	}
	// Catalog metadata must remain shared with callers.
	if _, ok := action.Lookup("k8s_delete_pod"); !ok {
		t.Fatal("missing catalog entry")
	}
}

func TestDeletePodLinkApplyRechecksOwner(t *testing.T) {
	controller := true
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "web", UID: "uid", ResourceVersion: "1", OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs", UID: "rs-uid", Controller: &controller}}}}
	for _, removeOwner := range []bool{false, true} {
		p, client := writeProvider(pod.DeepCopy())
		c := tool.Caller{Transport: "web"}
		in := deletePodInput{Namespace: "prod", Name: "web"}
		ctx := context.Background()
		plan, err := p.planDeletePod(ctx, c, in)
		if err != nil {
			t.Fatal(err)
		}
		if removeOwner {
			fresh := pod.DeepCopy()
			fresh.OwnerReferences = nil
			fresh.ResourceVersion = "2"
			if _, err := client.CoreV1().Pods("prod").Update(ctx, fresh, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		_, err = p.applyDeletePod(ctx, c, in, plan)
		if (err != nil) != removeOwner {
			t.Fatalf("removed owner=%v: %v", removeOwner, err)
		}
		deleted := false
		for _, a := range client.Actions() {
			if a.GetVerb() == "delete" {
				deleted = true
				opts := a.(kt.DeleteAction).GetDeleteOptions()
				if opts.Preconditions == nil || opts.Preconditions.ResourceVersion == nil || *opts.Preconditions.ResourceVersion != "1" {
					t.Fatal("missing owner snapshot precondition")
				}
			}
		}
		if deleted == removeOwner {
			t.Fatal("unexpected deletion")
		}
	}
}
