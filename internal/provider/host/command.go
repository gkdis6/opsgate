package host

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	systemctl  = "/usr/bin/systemctl"
	journalctl = "/usr/bin/journalctl"
	// maxOutput bounds what a host command may print before it is refused.
	maxOutput = 4 << 20
)

// runner runs one fixed host binary with argv only. Tests replace it.
type runner func(ctx context.Context, binary string, args ...string) (string, error)

// cappedWriter keeps at most limit bytes and remembers that more arrived, so
// a truncated secret (for example half a PEM block) is never passed on.
type cappedWriter struct {
	strings.Builder
	limit    int
	exceeded bool
}

func (w *cappedWriter) Write(b []byte) (int, error) {
	n := len(b)
	if room := w.limit - w.Len(); len(b) > room {
		b = b[:max(room, 0)]
		w.exceeded = true
	}
	w.Builder.Write(b)
	return n, nil
}

// runCommand never uses a shell: the binary is one of two fixed paths and the
// arguments are passed as argv. The process group is killed on timeout.
func runCommand(ctx context.Context, binary string, args ...string) (string, error) {
	if binary != systemctl && binary != journalctl {
		return "", fmt.Errorf("unsupported host binary")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0"}
	stdout, stderr := &cappedWriter{limit: maxOutput}, &cappedWriter{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded {
		return "", fmt.Errorf("host command output exceeded %d MiB", maxOutput>>20)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("%s exited with status %d", binary, exit.ExitCode())
		}
		return "", fmt.Errorf("%s failed: %w", binary, err)
	}
	return stdout.String(), nil
}
