package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunCommandDescendants(t *testing.T) {
	for _, cancelRunning := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel running group", false: "leader exits with open pipes"}[cancelRunning], func(t *testing.T) {
			fifo := filepath.Join(t.TempDir(), "ready")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			type processes struct {
				parent, child int
				err           error
			}
			ready := make(chan processes, 1)
			closed := make(chan error, 1)
			go func() {
				f, err := os.Open(fifo)
				if err != nil {
					ready <- processes{err: err}
					return
				}
				defer f.Close()
				var p processes
				_, p.err = fmt.Fscanln(f, &p.parent, &p.child)
				ready <- p
				_, err = io.Copy(io.Discard, f)
				closed <- err
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			script := `exec 3>"$1"; sleep 30 & printf '%s %s\n' "$$" "$!" >&3; `
			if cancelRunning {
				script += "wait"
			} else {
				script += "exit 0"
			}
			done := make(chan error, 1)
			go func() {
				_, err := runCommand(ctx, "/bin/sh", "-c", script, "sh", fifo)
				done <- err
			}()
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			select {
			case p := <-ready:
				if p.err != nil {
					t.Fatal(p.err)
				}
				t.Cleanup(func() {
					_ = syscall.Kill(-p.parent, syscall.SIGKILL)
					_ = syscall.Kill(p.parent, syscall.SIGKILL)
					_ = syscall.Kill(p.child, syscall.SIGKILL)
				})
			case <-deadline.C:
				t.Fatal("command did not become ready")
			}
			if cancelRunning {
				cancel()
			}
			select {
			case err := <-done:
				if cancelRunning {
					if err == nil {
						t.Fatal("canceled command succeeded")
					}
				} else if !errors.Is(err, exec.ErrWaitDelay) {
					t.Fatalf("got %v, want ErrWaitDelay", err)
				}
			case <-deadline.C:
				t.Fatal("command blocked on descendant output pipes")
			}
			if cancelRunning {
				// This pipe is independent of exec's output pipes and closes only when both processes exit.
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-deadline.C:
					t.Fatal("descendant survived cancellation")
				}
			}
		})
	}
}
