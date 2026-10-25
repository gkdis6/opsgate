//go:build !linux

package devrunner

import (
	"context"
	"os"
	"time"
)

func git(context.Context, string, int, ...string) (string, bool, error) {
	return "", false, errRequiresLinux
}

func (r *runner) execute(_ context.Context, t Task) Task {
	t.State, t.Error, t.Finished = "failed", errRequiresLinux.Error(), time.Now().UTC()
	return t
}

func readRegular(*os.Root, string, int64, int) ([]byte, error) {
	return nil, errRequiresLinux
}
