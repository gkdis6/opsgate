package kube

import (
	"context"
	"fmt"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
)

type registry struct {
	clusters []*provider
	byName   map[string]*provider
}

type overviewInput struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"Cluster name; required when several clusters are configured"`
}

type clusterInput interface{ clusterName() string }

func (in serviceCheckInput) clusterName() string { return in.Cluster }

func (in overviewInput) clusterName() string  { return in.Cluster }
func (in listInput) clusterName() string      { return in.Cluster }
func (in envInput) clusterName() string       { return in.Cluster }
func (in getInput) clusterName() string       { return in.Cluster }
func (in eventsInput) clusterName() string    { return in.Cluster }
func (in logsInput) clusterName() string      { return in.Cluster }
func (in rolloutInput) clusterName() string   { return in.Cluster }
func (in scaleInput) clusterName() string     { return in.Cluster }
func (in deletePodInput) clusterName() string { return in.Cluster }

func (r *registry) names() string {
	names := make([]string, len(r.clusters))
	for i, p := range r.clusters {
		names[i] = p.cfg.Name
	}
	return strings.Join(names, ", ")
}

// clustersNote names the clusters in tool descriptions so clients can pick one.
func (r *registry) clustersNote() string {
	if len(r.clusters) == 1 {
		return ""
	}
	return " Configured clusters: " + r.names() + "; cluster is required."
}

func (r *registry) resolve(name string) (*provider, error) {
	if name == "" {
		if len(r.clusters) == 1 {
			return r.clusters[0], nil
		}
		return nil, tool.Denied("cluster is required; configured clusters: %s", r.names())
	}
	if p := r.byName[name]; p != nil {
		return p, nil
	}
	return nil, tool.Denied("unknown cluster; configured clusters: %s", r.names())
}

func (r *registry) any(enabled func(*provider) bool) bool {
	for _, p := range r.clusters {
		if enabled(p) {
			return true
		}
	}
	return false
}

func clusterRead[In clusterInput](r *registry, read func(*provider, context.Context, tool.Caller, In) (string, error)) tool.Handler[In] {
	return func(ctx context.Context, c tool.Caller, in In) (string, error) {
		p, err := r.resolve(in.clusterName())
		if err != nil {
			return "", err
		}
		out, err := read(p, ctx, c, in)
		if err != nil {
			return "", err
		}
		return "cluster: " + p.cfg.Name + "\n" + out, nil
	}
}

func clusterWrite[In interface {
	clusterInput
	tool.WriteInput
}](r *registry,
	plan func(*provider, context.Context, tool.Caller, In) (tool.Plan, error),
	apply func(*provider, context.Context, tool.Caller, In, tool.Plan) (string, error),
) tool.WriteHandler[In] {
	return tool.WriteHandler[In]{
		Plan: func(ctx context.Context, c tool.Caller, in In) (tool.Plan, error) {
			p, err := r.resolve(in.clusterName())
			if err != nil {
				return tool.Plan{}, err
			}
			preview, err := plan(p, ctx, c, in)
			if err != nil {
				return tool.Plan{}, err
			}
			preview.Summary = fmt.Sprintf("Cluster %q: %s", p.cfg.Name, preview.Summary)
			preview.Target = fmt.Sprintf("Kubernetes cluster %q", p.cfg.Name)
			preview.NoWindow = p.cfg.Protected
			return preview, nil
		},
		Apply: func(ctx context.Context, c tool.Caller, in In, preview tool.Plan) (string, error) {
			p, err := r.resolve(in.clusterName())
			if err != nil {
				return "", err
			}
			if preview.Target != fmt.Sprintf("Kubernetes cluster %q", p.cfg.Name) {
				return "", tool.Denied("cluster changed since preview")
			}
			out, err := apply(p, ctx, c, in, preview)
			if err != nil {
				return "", err
			}
			return "cluster: " + p.cfg.Name + "\n" + out, nil
		},
	}
}
