package kube

import (
	"fmt"
	"path"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type rolloutInput struct {
	tool.Approval
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Kind      string `json:"kind" jsonschema:"Required: deployment, statefulset, or daemonset."`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace."`
	Name      string `json:"name" jsonschema:"Required allowlisted resource name."`
}

type scaleInput struct {
	tool.Approval
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Kind      string `json:"kind" jsonschema:"Required: deployment or statefulset."`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace."`
	Name      string `json:"name" jsonschema:"Required allowlisted resource name."`
	Replicas  int32  `json:"replicas" jsonschema:"Required desired replicas; minimum zero, maximum configured max_replicas."`
}

type deletePodInput struct {
	tool.Approval
	Cluster   string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
	Namespace string `json:"namespace" jsonschema:"Required allowed namespace."`
	Name      string `json:"name" jsonschema:"Required allowlisted pod name."`
}

func (p *provider) validateWrites() error {
	if p.cfg.MaxReplicas < 0 {
		return fmt.Errorf("max_replicas must not be negative")
	}
	for _, patterns := range [][]string{p.cfg.RolloutRestart, p.cfg.Scale, p.cfg.DeletePod} {
		for _, pattern := range patterns {
			if len(pattern) > 512 || strings.Count(pattern, "/") != 1 {
				return fmt.Errorf("write allowlist must contain namespace/name patterns")
			}
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("invalid write allowlist pattern: %w", err)
			}
		}
	}
	return nil
}

func (p *provider) writeAllowed(ns, name string, patterns []string) error {
	// Validate bounds before formatting errors or making any API request.
	if len(ns) > 63 || len(name) > 253 {
		return fmt.Errorf("invalid resource target")
	}
	if err := p.checkNamespace(ns); err != nil {
		return err
	}
	if err := validName(name, false); err != nil {
		return err
	}
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, ns+"/"+name); ok {
			return nil
		}
	}
	return tool.Denied("resource is not allowed")
}

func writeKind(kind string, daemon bool) (resource, error) {
	switch kind {
	case "deployment", "statefulset":
	case "daemonset":
		if !daemon {
			return resource{}, fmt.Errorf("unsupported write kind")
		}
	default:
		return resource{}, fmt.Errorf("unsupported write kind")
	}
	return resolveKind(kind)
}

func (r *registry) registerWrites(s *mcp.Server, env *tool.Env, note string) {
	if r.any(func(p *provider) bool { return len(p.cfg.RolloutRestart) > 0 }) {
		tool.AddWrite(s, env, tool.WriteSpec{Name: "k8s_rollout_restart", Title: "Restart Kubernetes workload", Description: "Select cluster and restart an allowlisted deployment, statefulset or daemonset by updating its pod template after human approval." + note, Direct: true}, clusterWrite(r, (*provider).planRollout, (*provider).applyRollout))
	}
	if r.any(func(p *provider) bool { return len(p.cfg.Scale) > 0 }) {
		tool.AddWrite(s, env, tool.WriteSpec{Name: "k8s_scale", Title: "Scale Kubernetes workload", Description: "Select cluster and scale an allowlisted deployment or statefulset within max_replicas after human approval." + note, Idempotent: true}, clusterWrite(r, (*provider).planScale, (*provider).applyScale))
	}
	if r.any(func(p *provider) bool { return len(p.cfg.DeletePod) > 0 }) {
		tool.AddWrite(s, env, tool.WriteSpec{Name: "k8s_delete_pod", Title: "Delete Kubernetes pod", Description: "Select cluster and delete the exact allowlisted pod shown in the human approval preview, protected by its UID." + note}, clusterWrite(r, (*provider).planDeletePod, (*provider).applyDeletePod))
	}
}
