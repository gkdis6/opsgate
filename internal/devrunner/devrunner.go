// Package devrunner runs coding agents in isolated git worktrees on behalf
// of the opsgate dev provider, over a Unix socket.
package devrunner

import (
	"context"

	"github.com/gkdis6/opsgate/internal/config"
)

// Serve runs the runner until ctx is done.
func Serve(ctx context.Context, cfg config.DevRunner) error {
	// TODO(codex): implement.
	return nil
}
