// Package dev exposes coding-agent tasks run by `opsgate dev-runner`.
package dev

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/tool"
)

// Register adds the dev tools to s.
func Register(s *mcp.Server, env *tool.Env, cfg config.Dev) error {
	// TODO(codex): implement.
	return nil
}
