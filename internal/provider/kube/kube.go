// Package kube provides read-only Kubernetes inspection.
package kube

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// Register adds the kube tools to s.
func Register(s *mcp.Server, env *tool.Env, cfg config.Kubernetes) error {
	// TODO(codex): implement.
	return nil
}
