package devrunner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

type boundedWriter struct {
	mu        sync.Mutex
	w         io.Writer
	left      int
	truncated bool
}

func (w *boundedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(b)
	if len(b) > w.left {
		b = b[:w.left]
		w.truncated = true
	}
	written, err := w.w.Write(b)
	w.left -= written
	if err != nil {
		return written, err
	}
	return n, nil // Drain excess output so a noisy child cannot block forever.
}

// Always reap the leader and terminate its process group, including children
// retaining stdout after the leader exits. No shell interprets command arguments.
func runProcess(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return ctx.Err()
	}
}

func git(ctx context.Context, repo string, limit int, args ...string) (string, bool, error) {
	argv := []string{"-c", "core.hooksPath=/dev/null", "-c", "safe.directory=" + repo, "-c", "core.fsmonitor=false", "-c", "commit.gpgsign=false", "-C", repo}
	cmd := exec.CommandContext(ctx, "/usr/bin/git", append(argv, args...)...)
	// Cancellation is handled for the whole group by runProcess.
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = 10 * time.Second
	cmd.Env = []string{"HOME=/nonexistent", "PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=opsgate dev-runner", "GIT_AUTHOR_EMAIL=dev-runner@localhost", "GIT_COMMITTER_NAME=opsgate dev-runner", "GIT_COMMITTER_EMAIL=dev-runner@localhost"}
	var out bytes.Buffer
	w := &boundedWriter{w: &out, left: limit}
	cmd.Stdout, cmd.Stderr = w, w
	err := runProcess(ctx, cmd)
	if err != nil {
		return "", w.truncated, fmt.Errorf("git command failed")
	}
	return out.String(), w.truncated, nil
}

func (r *runner) execute(ctx context.Context, t Task) Task {
	p, _ := r.project(t.Project)
	dir := r.dir(t.ID)
	worktree := filepath.Join(dir, "worktree")
	finish := func(state, msg string) Task {
		if ctx.Err() == context.DeadlineExceeded {
			state = "timed_out"
		} else if ctx.Err() != nil {
			state = "canceled"
		}
		t.State, t.Error, t.Finished = state, msg, time.Now().UTC()
		return t
	}
	base, truncated, err := git(ctx, p.Path, 256, "rev-parse", "--verify", "--end-of-options", p.BaseRef+"^{commit}")
	if err != nil || truncated {
		return finish("failed", "cannot resolve project base commit")
	}
	t.BaseCommit = strings.TrimSpace(base)
	if _, _, err = git(ctx, p.Path, MaxReadBytes, "worktree", "add", "-b", t.Branch, worktree, t.BaseCommit); err != nil {
		return finish("failed", "cannot create task worktree")
	}
	taskRoot, err := r.root.OpenRoot("tasks/" + t.ID)
	if err != nil {
		return finish("failed", "cannot open task directory")
	}
	defer taskRoot.Close()
	link, err := readRegular(taskRoot, "worktree/.git", 0, 4097)
	if err != nil || len(link) > 4096 {
		return finish("failed", "cannot inspect worktree metadata")
	}
	requirement, err := readRegular(taskRoot, "prompt.txt", 0, MaxPromptBytes+1)
	if err != nil || len(requirement) > MaxPromptBytes {
		return finish("failed", "cannot open prompt")
	}
	// Publish the pinned commit while running so diff is meaningful before exit.
	r.mu.Lock()
	if current := r.find(t.ID); current != nil {
		current.BaseCommit = t.BaseCommit
		_ = r.save()
	}
	r.mu.Unlock()
	log, err := taskRoot.OpenFile("log.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return finish("failed", "cannot create task log")
	}
	w := &boundedWriter{w: log, left: maxLogBytes}
	a := r.cfg.Agents[t.Agent]
	argv := make([]string, len(a.Command))
	for i, arg := range a.Command {
		argv[i] = strings.NewReplacer("{workdir}", worktree, "{output}", filepath.Join(dir, "output.txt")).Replace(arg)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Cancel = func() error { return nil }
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = worktree, bytes.NewReader(requirement), w, w
	cmd.Env = append([]string{"HOME=" + os.Getenv("HOME"), "PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TERM=dumb"}, a.Env...)
	runErr := runProcess(ctx, cmd)
	closeErr := log.Close()
	// OpenRoot prevents agent-created symlinks from escaping the task directory;
	// O_NONBLOCK and a regular-file check also reject FIFOs and devices.
	if msg, e := readMessage(taskRoot, "output.txt", MaxReadBytes, false); e == nil {
		t.Message = msg
	} else if msg, e := readMessage(taskRoot, "log.txt", 8<<10, true); e == nil {
		t.Message = msg
	}
	if closeErr != nil {
		return finish("failed", "cannot save task log")
	}
	// Preserve edits after cancel/timeout once the agent's group has stopped.
	// Finalization has its own short bound so shutdown can still complete.
	finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer finalCancel()
	currentLink, linkErr := readRegular(taskRoot, "worktree/.git", 0, 4097)
	if linkErr != nil || !bytes.Equal(currentLink, link) {
		return finish("failed", "agent changed worktree metadata")
	}
	branch, _, branchErr := git(finalCtx, worktree, 256, "symbolic-ref", "--quiet", "HEAD")
	if branchErr != nil || strings.TrimSpace(branch) != "refs/heads/"+t.Branch {
		return finish("failed", "agent changed task branch")
	}
	// Commit any edits even when the configured agent exits unsuccessfully.
	if _, _, err = git(finalCtx, worktree, MaxReadBytes, "add", "-A", "--", "."); err != nil {
		return finish("failed", "cannot stage task changes")
	}
	status, _, err := git(finalCtx, worktree, MaxReadBytes, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return finish("failed", "cannot inspect task changes")
	}
	if status != "" {
		line, _, _ := strings.Cut(string(requirement), "\n")
		line = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, line)
		runes := []rune(line)
		if len(runes) > 72 {
			line = string(runes[:72])
		}
		if _, _, err = git(finalCtx, worktree, MaxReadBytes, "commit", "--no-verify", "-m", "opsgate task "+t.ID+": "+line); err != nil {
			return finish("failed", "cannot commit task changes")
		}
	}
	if ctx.Err() != nil {
		return finish("canceled", "")
	}
	if runErr != nil {
		return finish("failed", "agent exited unsuccessfully")
	}
	return finish("succeeded", "")
}

func readRegular(root *os.Root, path string, offset int64, limit int) ([]byte, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, int64(limit)))
}
func readMessage(root *os.Root, path string, limit int, tail bool) (string, error) {
	// Scan the complete source before selecting a fragment. Larger sources
	// cannot be redacted safely within the read bound.
	b, err := readRegular(root, path, 0, maxLogBytes+1)
	if err != nil {
		return "", err
	}
	if len(b) > maxLogBytes {
		return "task message omitted: source exceeds 20 MiB safe read limit", nil
	}
	if len(b) <= limit {
		return redactedText(b), nil
	}
	start, end := 0, min(limit, len(b))
	if tail {
		start, end = max(0, len(b)-limit), len(b)
	}
	return redactedFragment(string(b), start, end), nil
}
