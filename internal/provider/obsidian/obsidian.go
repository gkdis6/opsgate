// Package obsidian provides tools for a Markdown note vault.
package obsidian

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// Register adds the vault tools to s.
func Register(s *mcp.Server, env *tool.Env, cfg config.Obsidian) error {
	// TODO(codex): implement.
	return nil
}
