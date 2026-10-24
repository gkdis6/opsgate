package kube

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRegistration(t *testing.T) {
	for _, logs := range []bool{false, true} {
		t.Run(map[bool]string{false: "without logs", true: "with logs"}[logs], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			logger, err := audit.Open("")
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			env := &tool.Env{Audit: logger, Timeout: time.Second, MaxOutputBytes: 65536, Fallback: &tool.Caller{User: "test", Scopes: []string{config.ScopeRead}}}
			clientset := fake.NewClientset()
			if err := register(server, env, config.Kubernetes{AllowLogs: logs, Namespaces: []string{"*"}}, clientset); err != nil {
				t.Fatal(err)
			}
			if len(clientset.Actions()) != 0 {
				t.Fatal("registration contacted cluster")
			}
			a, b := mcp.NewInMemoryTransports()
			ss, err := server.Connect(ctx, a, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			session, err := client.Connect(ctx, b, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			tools, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, spec := range tools.Tools {
				names = append(names, spec.Name)
				if !spec.Annotations.ReadOnlyHint || *spec.Annotations.DestructiveHint {
					t.Fatal(spec)
				}
			}
			if slices.Contains(names, "k8s_logs") != logs || len(names) != 4+map[bool]int{false: 0, true: 1}[logs] {
				t.Fatal(names)
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "k8s_list", Arguments: map[string]any{"kind": "secrets"}})
			if err != nil || !result.IsError {
				t.Fatal(result, err)
			}
		})
	}
}

func TestRegisterConfig(t *testing.T) {
	for _, tt := range []struct {
		name, extra, context string
		fail                 bool
	}{
		{"lazy", "", "chosen", false}, {"missing context", "", "missing", true},
		{"exec plugin", "    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: never-execute\n      interactiveMode: Never\n", "chosen", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			content := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://cluster.invalid
contexts:
- name: chosen
  context:
    cluster: test
    user: test
users:
- name: test
  user:
    token: unused
` + tt.extra
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			err := Register(s, &tool.Env{}, config.Kubernetes{Kubeconfig: path, Context: tt.context})
			if (err != nil) != tt.fail {
				t.Fatal(err)
			}
			if strings.Contains(tt.extra, "exec:") && err != nil && !strings.Contains(err.Error(), "plugins") {
				t.Fatal(err)
			}
		})
	}
}
