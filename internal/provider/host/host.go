// Package host provides systemd units, journald logs and basic host metrics.
package host

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// Register adds the host tools to s.
func Register(s *mcp.Server, env *tool.Env, cfg config.Host) error {
	// TODO(codex): implement.
	return nil
}
