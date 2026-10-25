// Package devrunner runs configured coding agents in git worktrees over a
// peer-authenticated Unix socket. The runner must use a dedicated OS account.
// Only the opsgate server and trusted runner account may access the socket:
// socket peers can assert any caller identity using X-Opsgate-User. Coding
// agents and other untrusted processes must not have access to that socket.
package devrunner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/gkdis6/opsgate/internal/config"
)

type runner struct {
	mu         sync.Mutex
	cfg        config.DevRunner
	root       *os.Root
	tasks      []*Task
	cancels    map[string]context.CancelFunc
	wake       chan struct{}
	ctx        context.Context
	stop       context.CancelFunc
	storageErr error
}

// Serve runs the runner until ctx is done, canceling and reaping its processes
// before returning. Zero-valued limits receive the documented defaults.
func Serve(ctx context.Context, cfg config.DevRunner) error {
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Hour
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.MaxTasks == 0 {
		cfg.MaxTasks = 100
	}
	cfg.Admins = slices.Clone(cfg.Admins)
	cfg.Projects = slices.Clone(cfg.Projects)
	for i := range cfg.Projects {
		if cfg.Projects[i].BaseRef == "" {
			cfg.Projects[i].BaseRef = "HEAD"
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	g, err := user.LookupGroup(cfg.SocketGroup)
	if err != nil {
		return fmt.Errorf("resolve socket group: %w", err)
	}
	gid, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid socket group")
	}
	if err = os.MkdirAll(cfg.WorkDir, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(cfg.WorkDir)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := root.OpenFile(".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("runner already using work_dir: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err = root.MkdirAll("tasks", 0700); err != nil {
		return err
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	r := &runner{cfg: cfg, root: root, ctx: runCtx, stop: stop, cancels: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1)}
	if err = r.load(); err != nil {
		return err
	}
	if err = prepareSocket(cfg.Socket); err != nil {
		return err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.Socket, Net: "unix"})
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chown(cfg.Socket, -1, int(gid)); err != nil {
		return err
	}
	if err = os.Chmod(cfg.Socket, 0660); err != nil {
		return err
	}
	srv := &http.Server{Handler: r.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	var workers sync.WaitGroup
	for i := 0; i < cfg.MaxConcurrent; i++ {
		workers.Go(r.worker)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peerListener{l, uint32(gid)}) }()
	var serveErr error
	select {
	case <-runCtx.Done():
	case serveErr = <-done:
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = srv.Close()
	workers.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if t.State == "queued" {
			t.State = "canceled"
			t.Finished = time.Now().UTC()
		}
	}
	if err := r.save(); err != nil {
		return err
	}
	if r.storageErr != nil {
		return r.storageErr
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

func prepareSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket at runner socket path")
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		c.Close()
		return fmt.Errorf("runner socket is already active")
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot establish whether runner socket is stale: %w", err)
	}
	return os.Remove(path)
}

func (r *runner) dir(id string) string { return filepath.Join(r.cfg.WorkDir, "tasks", id) }
func (r *runner) project(name string) (config.DevProject, bool) {
	for _, p := range r.cfg.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return config.DevProject{}, false
}
func (r *runner) find(id string) *Task {
	for _, t := range r.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
func (r *runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *runner) worker() {
	for {
		r.mu.Lock()
		var task *Task
		if r.ctx.Err() == nil && r.storageErr == nil {
			for _, t := range r.tasks {
				if t.State == "queued" {
					task = t
					break
				}
			}
		}
		if task == nil {
			r.mu.Unlock()
			select {
			case <-r.ctx.Done():
				return
			case <-r.wake:
				continue
			}
		}
		ctx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
		r.cancels[task.ID] = cancel
		task.State, task.Started = "running", time.Now().UTC()
		if err := r.save(); err != nil {
			cancel()
			delete(r.cancels, task.ID)
			r.mu.Unlock()
			return
		}
		snapshot := *task
		r.signal()
		r.mu.Unlock()
		result := r.execute(ctx, snapshot)
		cancel()
		r.mu.Lock()
		delete(r.cancels, task.ID)
		*task = result
		_ = r.save()
		r.mu.Unlock()
	}
}
