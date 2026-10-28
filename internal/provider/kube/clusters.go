package kube

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/tool"
	"k8s.io/apimachinery/pkg/version"
)

func (r *registry) clusterList(ctx context.Context, _ tool.Caller, _ struct{}) (string, error) {
	var b strings.Builder
	for i, p := range r.clusters {
		if i > 0 {
			b.WriteString("\n")
		}
		cfg := p.cfg
		contextName := cfg.Context
		if contextName == "" {
			contextName = "current"
		}
		if p.inCluster {
			contextName = "in-cluster"
		}
		fmt.Fprintf(&b, "cluster: %s\ncontext: %s\nprotected: %t\nallowed namespaces: %v\ndenied namespaces: %v\nlogs: %s\n", cfg.Name, contextName, cfg.Protected, cfg.Namespaces, cfg.DenyNamespaces, onOff(cfg.AllowLogs))
		for _, action := range []struct {
			name     string
			patterns []string
		}{
			{"k8s_rollout_restart", cfg.RolloutRestart}, {"k8s_scale", cfg.Scale}, {"k8s_delete_pod", cfg.DeletePod},
		} {
			fmt.Fprintf(&b, "%s: %s %v\n", action.name, onOff(len(action.patterns) > 0), action.patterns)
		}
		fmt.Fprintf(&b, "max_replicas: %d\nreachability: %s\n", cfg.MaxReplicas, p.reachability(ctx))
	}
	return b.String(), nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

var serverVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9.+-]*$`)

func (p *provider) reachability(ctx context.Context) string {
	if err := ctx.Err(); err != nil {
		return "unreachable: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		version *version.Info
		err     error
	}
	done := make(chan result, 1)
	go func() {
		v, err := p.client.Discovery().ServerVersion()
		done <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		return "unreachable: " + ctx.Err().Error()
	case res := <-done:
		if res.err != nil {
			// Discovery errors can embed server URLs, credentials or local paths.
			// Report a fixed error rather than reflecting transport diagnostics.
			return "unreachable: discovery ServerVersion failed"
		}
		if res.version == nil {
			return "unreachable: empty server version"
		}
		if len(res.version.GitVersion) > 128 || !serverVersion.MatchString(res.version.GitVersion) {
			return "unreachable: invalid server version"
		}
		return fmt.Sprintf("reachable (%s)", res.version.GitVersion)
	}
}
