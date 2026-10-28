package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
)

func TestClusterInventory(t *testing.T) {
	cfg, clients := clusterFixture()
	cfg.Clusters[0].Kubeconfig = "/private/kubeconfig"
	cfg.Clusters[0].Context = "kind-local"
	cfg.Clusters[1].Kubeconfig = "/private/production"
	clients["kind"].Discovery().(*discoveryfake.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.31.2"}
	clients["prod"].(*fake.Clientset).PrependReactor("get", "version", func(kt.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Get https://user:secret@prod.invalid/version: /private/production: token hidden")
	})
	cs := clusterSession(t, cfg, clients, nil)
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "k8s_clusters", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	out := result.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{
		"cluster: kind\ncontext: kind-local\nprotected: false", "allowed namespaces: [apps]", "logs: on",
		"k8s_rollout_restart: on [apps/*]", "k8s_scale: on [apps/*]", "k8s_delete_pod: on [apps/*]", "max_replicas: 10",
		"reachability: reachable (v1.31.2)", "cluster: prod\ncontext: current\nprotected: true", "denied namespaces: [private]",
		"logs: off", "k8s_scale: off []", "reachability: unreachable:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if !strings.HasPrefix(out, "cluster: kind\n") || strings.Index(out, "cluster: kind") > strings.Index(out, "cluster: prod") {
		t.Fatal(out)
	}
	for _, secret := range []string{"/private/", "https://", "prod.invalid", "secret", "token hidden"} {
		if strings.Contains(out, secret) {
			t.Fatalf("inventory exposed %q: %s", secret, out)
		}
	}
	for name, client := range clients {
		actions := client.(*fake.Clientset).Actions()
		if len(actions) != 1 || actions[0].GetResource().Resource != "version" {
			t.Fatalf("%s actions = %v", name, actions)
		}
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	client := fake.NewClientset()

	release := make(chan struct{})
	started := make(chan struct{})
	finished := make(chan struct{})
	client.PrependReactor("get", "version", func(kt.Action) (bool, runtime.Object, error) {
		defer close(finished)
		close(started)
		<-release
		return true, nil, errors.New("offline")
	})
	p := &provider{client: client}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() { done <- p.reachability(ctx) }()
	<-started
	start := time.Now()
	cancel()
	out := <-done
	close(release)
	<-finished
	if time.Since(start) > time.Second || out != "unreachable: context canceled" {
		t.Fatal(out)
	}
	client.ClearActions()
	if out := p.reachability(ctx); out != "unreachable: context canceled" || len(client.Actions()) > 0 {
		t.Fatal(out, client.Actions())
	}

}

func TestInClusterInventory(t *testing.T) {
	p := &provider{client: fake.NewClientset(), inCluster: true}
	out, err := (&registry{clusters: []*provider{p}}).clusterList(context.Background(), tool.Caller{}, struct{}{})
	if err != nil || !strings.Contains(out, "context: in-cluster") {
		t.Fatal(out, err)
	}
}

func TestDiscoveryVersionIsUntrusted(t *testing.T) {
	client := fake.NewClientset()
	client.Discovery().(*discoveryfake.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "https://user:password@cluster.invalid/version"}
	p := &provider{client: client}
	if out := p.reachability(context.Background()); out != "unreachable: invalid server version" {
		t.Fatal(out)
	}
}
