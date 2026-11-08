package kube

import (
	"context"
	"encoding/json"

	"github.com/gkdis6/opsgate/internal/action"
	"github.com/gkdis6/opsgate/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type actionLinkInput struct {
	Tool      string         `json:"tool" jsonschema:"Catalog action tool name"`
	Arguments map[string]any `json:"arguments" jsonschema:"Exact action arguments; no approval_id or source"`
	Source    string         `json:"source,omitempty" jsonschema:"ai (default) or rule"`
}

// registerActionLink uses the existing per-cluster approval origin. It exposes
// no write capability: ValidateLink calls only the side-effect-free Plan.
func (r *registry) registerActionLink(s *mcp.Server, env *tool.Env) {
	enabled := r.any(func(p *provider) bool {
		if p.cfg.ActionApprovalURL == "" {
			return false
		}
		for name, patterns := range map[string][]string{"k8s_rollout_restart": p.cfg.RolloutRestart, "k8s_restore_image": p.cfg.RestoreImage, "k8s_scale": p.cfg.Scale, "k8s_delete_pod": p.cfg.DeletePod} {
			if len(patterns) > 0 && env.Direct(name) != nil {
				return true
			}
		}
		return false
	})
	if !enabled {
		return
	}
	tool.Add(s, env, tool.Spec{Name: "action_link", Title: "Suggest an approval action", ReadOnly: true, Description: "Read-only: validate a server catalog action and its current target, then return an approval URL, label and plan summary. A person must sign in and approve every action. Prefer the least invasive action. Never applies changes. source defaults to ai."}, func(ctx context.Context, c tool.Caller, in actionLinkInput) (string, error) {
		if err := action.Validate(in.Tool, in.Arguments); err != nil {
			return "", tool.Denied("%s", err)
		}
		// An unverified history image is offered only as the undo of a restore
		// a person just approved, never as a fresh suggestion.
		if revert, _ := in.Arguments["revert"].(bool); revert {
			return "", tool.Denied("revert is offered only as an undo link")
		}
		d := env.Direct(in.Tool)
		if d == nil {
			return "", tool.Denied("link action is disabled")
		}
		source, err := action.Source(in.Source, "ai")
		if err != nil {
			return "", err
		}
		cluster, _ := in.Arguments["cluster"].(string)
		p, err := r.resolve(cluster)
		if err != nil {
			return "", err
		}
		if p.cfg.ActionApprovalURL == "" {
			return "", tool.Denied("no approval base URL for this cluster")
		}
		// Bind a defaulted cluster explicitly so a link cannot resolve a
		// different default on the public approval server.
		arguments := make(map[string]any, len(in.Arguments)+1)
		for key, value := range in.Arguments {
			arguments[key] = value
		}
		arguments["cluster"] = p.cfg.Name
		raw, err := json.Marshal(arguments)
		if err != nil {
			return "", err
		}
		_, summary, err := d.ValidateLink(ctx, c, raw)
		if err != nil {
			return "", err
		}
		link, err := action.URL(p.cfg.ActionApprovalURL, in.Tool, arguments, source)
		if err != nil {
			return "", err
		}
		entry, _ := action.Lookup(in.Tool)
		out, err := json.Marshal(map[string]string{"url": link, "label": entry.Label("en"), "summary": summary})
		return string(out), err
	})
}
