package devrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gkdis6/opsgate/internal/config"
)

type harness struct {
	cfg    config.DevRunner
	client *http.Client
	stop   func()
	repo   string
	memory bool
}

func testGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, _, err := git(context.Background(), repo, 1<<20, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}
func runnerConfig(t *testing.T, script string) (config.DevRunner, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "og-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", ".")
	testGit(t, repo, "commit", "-m", "base")
	agent := filepath.Join(dir, "agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nset -eu\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroupId(fmt.Sprint(os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	return config.DevRunner{Socket: filepath.Join(dir, "run.sock"), SocketGroup: group.Name, WorkDir: filepath.Join(dir, "state"), Projects: []config.DevProject{{Name: "app", Path: repo, BaseRef: "main"}}, Agents: map[string]config.DevAgent{"fake": {Command: []string{agent, "{output}"}}}, Timeout: 5 * time.Second, MaxConcurrent: 1, MaxTasks: 100}, repo
}

type memoryTransport struct{ handler http.Handler }

func (m memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		req.Body = http.NoBody
	}
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}
func launchCore(t *testing.T, cfg config.DevRunner) *harness {
	t.Helper()
	root, err := os.OpenRoot(cfg.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{cfg: cfg, root: root, ctx: ctx, stop: cancel, cancels: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1)}
	if err := r.load(); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < cfg.MaxConcurrent; i++ {
		workers.Go(r.worker)
	}
	h := &harness{cfg: cfg, repo: cfg.Projects[0].Path, memory: true, client: &http.Client{Transport: memoryTransport{r.handler()}}}
	var once sync.Once
	h.stop = func() {
		once.Do(func() {
			cancel()
			workers.Wait()
			r.mu.Lock()
			for _, task := range r.tasks {
				if task.State == "queued" {
					task.State = "canceled"
					task.Finished = time.Now().UTC()
				}
			}
			if err := r.save(); err != nil {
				t.Error(err)
			}
			r.mu.Unlock()
			root.Close()
		})
	}
	t.Cleanup(h.stop)
	return h
}
func launch(t *testing.T, cfg config.DevRunner) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var serveErr error
	go func() { serveErr = Serve(ctx, cfg); close(done) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}
	h := &harness{cfg: cfg, repo: cfg.Projects[0].Path, client: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
	var once sync.Once
	h.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
				if serveErr != nil {
					t.Errorf("Serve: %v", serveErr)
				}
			case <-time.After(20 * time.Second):
				t.Error("runner did not stop")
			}
			transport.CloseIdleConnections()
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			cancel()
			transport.CloseIdleConnections()
			if errors.Is(serveErr, syscall.EPERM) {
				t.Log("Unix sockets denied by sandbox; testing engine over in-memory HTTP")
				return launchCore(t, cfg)
			}
			t.Fatalf("runner startup: %v", serveErr)
		default:
		}
		resp, err := h.client.Get("http://runner/v1/info")
		if err == nil {
			resp.Body.Close()
			t.Cleanup(h.stop)
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.stop()
	t.Fatal("runner did not listen")
	return nil
}
func (h *harness) request(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if raw, ok := body.(string); ok {
		reader = strings.NewReader(raw)
	} else if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://runner"+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Opsgate-User", "operator")
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode
}
func (h *harness) start(t *testing.T, prompt string) Task {
	t.Helper()
	var task Task
	if code := h.request(t, "POST", "/v1/tasks", StartRequest{Project: "app", Agent: "fake", Prompt: prompt, RequestedBy: "operator"}, &task); code != 202 {
		t.Fatalf("start: %d %+v", code, task)
	}
	return task
}
func (h *harness) wait(t *testing.T, id, state string) Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var task Task
		if code := h.request(t, "GET", "/v1/tasks/"+id, nil, &task); code != 200 {
			t.Fatalf("status: %d", code)
		}
		if task.State == state {
			return task
		}
		if finished(task.State) {
			t.Fatalf("want %s, got %+v", state, task)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task did not reach %s", state)
	return Task{}
}

func TestRunnerExecution(t *testing.T) {
	for _, tc := range []struct{ name, script, state, message string }{
		{"edit", "cat > requirement.txt; echo edited > file.txt; echo done", "succeeded", "done"},
		{"output", "cat >/dev/null; echo final > \"$1\"; echo log-only", "succeeded", "final"},
		{"failure", "cat >/dev/null; echo edited > file.txt; echo failed; exit 7", "failed", "failed"},
		{"stdin only", "test $# -eq 1; cat; test -z \"${LEAK_SENTINEL-}\"; test \"$TERM\" = dumb; test \"$LANG\" = C.UTF-8; test \"$EXTRA\" = configured", "succeeded", "private requirement"},
		{"symlink output", "cat >/dev/null; ln -s /etc/passwd \"$1\"; echo fallback", "succeeded", "fallback"},
		{"fifo output", "cat >/dev/null; mkfifo \"$1\"; echo fallback", "succeeded", "fallback"},
		{"metadata fifo", "cat >/dev/null; rm .git; mkfifo .git; echo rejected", "failed", "rejected"},
		{"metadata replaced", "cat >/dev/null; printf 'gitdir: /not/a/repo\\n' > .git; echo rejected", "failed", "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, repo := runnerConfig(t, tc.script)
			a := cfg.Agents["fake"]
			a.Env = []string{"EXTRA=configured"}
			cfg.Agents["fake"] = a
			t.Setenv("LEAK_SENTINEL", "must-not-leak")
			marker := filepath.Join(filepath.Dir(repo), "hook-ran")
			for _, hook := range []string{"pre-commit", "post-checkout", "post-commit"} {
				if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", hook), []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			h := launch(t, cfg)
			fi, err := os.Stat(cfg.Socket)
			if !h.memory && (err != nil || fi.Mode().Perm() != 0660) {
				t.Fatalf("socket mode: %v %v", fi, err)
			}
			task := h.start(t, "private requirement")
			task = h.wait(t, task.ID, tc.state)
			if !strings.Contains(task.Message, tc.message) || task.Branch != "opsgate/"+task.ID || !taskID.MatchString(task.ID) {
				t.Fatalf("task: %+v", task)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("git hook ran")
			}
			if got := testGit(t, repo, "branch", "--show-current"); got != "main" {
				t.Fatal(got)
			}
			if got := testGit(t, repo, "show", "main:file.txt"); got != "base" {
				t.Fatal(got)
			}
			if tc.name == "edit" || tc.name == "failure" {
				if got := testGit(t, repo, "show", task.Branch+":file.txt"); got != "edited" {
					t.Fatal(got)
				}
				if got := testGit(t, repo, "log", "-1", "--format=%an <%ae>", task.Branch); got != "opsgate dev-runner <dev-runner@localhost>" {
					t.Fatal(got)
				}
				var diff Diff
				if h.request(t, "GET", "/v1/tasks/"+task.ID+"/diff", nil, &diff) != 200 || !strings.Contains(diff.Text, "+edited") {
					t.Fatalf("diff: %+v", diff)
				}
			}
		})
	}
}

func TestRunnerCancelTimeoutFIFO(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := runnerConfig(t, "cat >/dev/null; echo sleeping; sleep 60")
			if mode == "timeout" {
				cfg.Timeout = time.Second
			}
			h := launch(t, cfg)
			first := h.start(t, "one")
			h.wait(t, first.ID, "running")
			second := h.start(t, "two")
			var current Task
			h.request(t, "GET", "/v1/tasks/"+second.ID, nil, &current)
			if current.State != "queued" {
				t.Fatalf("concurrency bound: %+v", current)
			}
			third := h.start(t, "three")
			h.request(t, "POST", "/v1/tasks/"+third.ID+"/cancel", nil, nil)
			h.wait(t, third.ID, "canceled")
			switch mode {
			case "cancel":
				h.request(t, "POST", "/v1/tasks/"+first.ID+"/cancel", nil, nil)
				h.wait(t, first.ID, "canceled")
				h.wait(t, second.ID, "running")
				h.request(t, "POST", "/v1/tasks/"+second.ID+"/cancel", nil, nil)
				h.wait(t, second.ID, "canceled")
			case "timeout":
				h.wait(t, first.ID, "timed_out")
				h.wait(t, second.ID, "running")
			case "shutdown":
				h.stop()
				b, err := os.ReadFile(filepath.Join(cfg.WorkDir, "tasks.json"))
				if err != nil {
					t.Fatal(err)
				}
				var tasks []Task
				if err = json.Unmarshal(b, &tasks); err != nil {
					t.Fatal(err)
				}
				for _, task := range tasks {
					if task.State != "canceled" {
						t.Fatalf("shutdown: %+v", task)
					}
				}
			}
		})
	}
}

func TestRunnerCapsAndValidation(t *testing.T) {
	cfg, _ := runnerConfig(t, "cat >/dev/null; head -c 22000000 /dev/zero | tr '\\000' x; head -c 400000 /dev/zero | tr '\\000' z > large.txt")
	h := launch(t, cfg)
	for _, body := range []string{
		`{"project":"app","agent":"fake","prompt":""}`,
		`{"project":"other","agent":"fake","prompt":"x"}`,
		`{"project":"app","agent":"other","prompt":"x"}`,
		`{"project":"app","agent":"fake","prompt":"x","command":["/bin/sh"]}`,
		`{"project":"app","agent":"fake","prompt":"x"} {}`,
		`null`,
		strings.Repeat("x", 300<<10),
	} {
		if code := h.request(t, "POST", "/v1/tasks", body, nil); code != 400 {
			t.Fatalf("invalid body accepted: %d", code)
		}
	}
	for _, in := range []StartRequest{
		{Project: "app", Agent: "fake", Prompt: strings.Repeat("x", MaxPromptBytes+1)},
		{Project: "app", Agent: "fake", Prompt: "ok", RequestedBy: strings.Repeat("x", 257)},
	} {
		if code := h.request(t, "POST", "/v1/tasks", in, nil); code != 400 {
			t.Fatal(code)
		}
	}
	task := h.start(t, strings.Repeat("p", MaxPromptBytes))
	task = h.wait(t, task.ID, "succeeded")
	fi, err := os.Stat(filepath.Join(cfg.WorkDir, "tasks", task.ID, "log.txt"))
	if err != nil || fi.Size() != maxLogBytes {
		t.Fatalf("log cap: %v %v", fi, err)
	}
	if len(task.Message) != 8<<10 {
		t.Fatal("log tail cap", len(task.Message))
	}
	var log Log
	if h.request(t, "GET", "/v1/tasks/"+task.ID+"/log?limit=262144", nil, &log) != 200 || len(log.Text) != MaxReadBytes || log.NextOffset != MaxReadBytes || !log.Truncated {
		t.Fatalf("log: %d %+v", len(log.Text), log.NextOffset)
	}
	var diff Diff
	if h.request(t, "GET", "/v1/tasks/"+task.ID+"/diff", nil, &diff) != 200 || len(diff.Text) != MaxReadBytes || !diff.Truncated {
		t.Fatal("diff cap", len(diff.Text), diff.Truncated)
	}
	for _, path := range []string{"/v1/tasks?limit=0", "/v1/tasks?limit=101", "/v1/tasks?limit=1&limit=2", "/v1/tasks?extra=x", "/v1/tasks/invalid", "/v1/tasks/" + task.ID + "/log?offset=-1", "/v1/tasks/" + task.ID + "/log?limit=262145", "/v1/tasks/" + task.ID + "/log?limit=1;extra=2"} {
		if code := h.request(t, "GET", path, nil, nil); code != 400 {
			t.Fatalf("%s: %d", path, code)
		}
	}
}

func TestRestartRetention(t *testing.T) {
	cfg, repo := runnerConfig(t, "cat >/dev/null; echo edit > file.txt")
	cfg.MaxTasks = 2
	h := launch(t, cfg)
	first := h.start(t, "first")
	h.wait(t, first.ID, "succeeded")
	second := h.start(t, "second")
	h.wait(t, second.ID, "succeeded")
	third := h.start(t, "third")
	h.wait(t, third.ID, "succeeded")
	if code := h.request(t, "GET", "/v1/tasks/"+first.ID, nil, nil); code != 404 {
		t.Fatal("old record retained", code)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, "tasks", first.ID)); !os.IsNotExist(err) {
		t.Fatal("old worktree retained", err)
	}
	testGit(t, repo, "rev-parse", "--verify", first.Branch)
	h.stop()
	path := filepath.Join(cfg.WorkDir, "tasks.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks []Task
	if err = json.Unmarshal(b, &tasks); err != nil {
		t.Fatal(err)
	}
	tasks[0].State = "running"
	tasks[1].State = "queued"
	b, _ = json.Marshal(tasks)
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	h = launch(t, cfg)
	for _, id := range []string{second.ID, third.ID} {
		h.wait(t, id, "interrupted")
	}
}

func TestPeerPredicate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		uid, gid uint32
		groups   []string
		err      error
		allow    bool
	}{
		{"self", 42, 1, nil, fmt.Errorf("lookup fails"), true},
		{"primary", 10, 7, nil, nil, true},
		{"supplementary", 10, 1, []string{"1", "7"}, nil, true},
		{"unlisted", 10, 1, []string{"1", "8"}, nil, false},
		{"lookup failure", 10, 1, []string{"7"}, fmt.Errorf("failed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowedPeer(tc.uid, tc.gid, 42, 7, func(uint32) ([]string, error) { return tc.groups, tc.err }); got != tc.allow {
				t.Fatal(got)
			}
		})
	}
}

func TestSocketAndLockSafety(t *testing.T) {
	cfg, _ := runnerConfig(t, "cat >/dev/null")
	if err := os.WriteFile(cfg.Socket, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Serve(context.Background(), cfg); err == nil {
		t.Fatal("removed regular file")
	}
	if b, _ := os.ReadFile(cfg.Socket); string(b) != "keep" {
		t.Fatal("file changed")
	}
	os.Remove(cfg.Socket)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.Socket, Net: "unix"})
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox denies Unix socket creation")
	}
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
	h := launch(t, cfg)
	if err := Serve(context.Background(), cfg); err == nil {
		t.Fatal("second runner accepted")
	}
	h.stop()
}

func TestProcessGroupTermination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 60 & wait")
	cmd.Cancel = func() error { return nil }
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := runProcess(ctx, cmd); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestConcurrentCapacityAndOutputFileCap(t *testing.T) {
	cfg, _ := runnerConfig(t, "cat >/dev/null; echo ready; while test ! -e \"$GATE\"; do sleep 0.01; done; head -c 400000 /dev/zero | tr '\\000' x > \"$1\"")
	cfg.MaxConcurrent = 2
	cfg.MaxTasks = 2
	gate := filepath.Join(filepath.Dir(cfg.Socket), "gate")
	a := cfg.Agents["fake"]
	a.Env = []string{"GATE=" + gate}
	cfg.Agents["fake"] = a
	h := launch(t, cfg)
	first := h.start(t, "one")
	second := h.start(t, "two")
	h.wait(t, first.ID, "running")
	h.wait(t, second.ID, "running")
	if code := h.request(t, "POST", "/v1/tasks", StartRequest{Project: "app", Agent: "fake", Prompt: "three"}, nil); code != 503 {
		t.Fatal("accepted beyond active capacity", code)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		task := h.wait(t, id, "succeeded")
		if len(task.Message) != MaxReadBytes {
			t.Fatalf("output file cap: %d", len(task.Message))
		}
	}
}

func TestPersistenceFailsClosed(t *testing.T) {
	cfg, _ := runnerConfig(t, "cat >/dev/null; touch should-not-run")
	if err := os.MkdirAll(filepath.Join(cfg.WorkDir, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	h := launchCore(t, cfg)
	// Renaming a regular temporary store over a directory must fail even for root.
	if err := os.Mkdir(filepath.Join(cfg.WorkDir, "tasks.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if code := h.request(t, "POST", "/v1/tasks", StartRequest{Project: "app", Agent: "fake", Prompt: "one"}, nil); code != 503 {
		t.Fatal("accepted with broken persistence", code)
	}
	if err := os.Remove(filepath.Join(cfg.WorkDir, "tasks.json")); err != nil {
		t.Fatal(err)
	}
	// The first failure remains sticky even after storage becomes writable again.
	if h.memory {
		if code := h.request(t, "POST", "/v1/tasks", StartRequest{Project: "app", Agent: "fake", Prompt: "two"}, nil); code != 503 {
			t.Fatal("dispatch resumed after persistence failure", code)
		}
	}
}

func TestCanceledEditsAreCommitted(t *testing.T) {
	cfg, repo := runnerConfig(t, "cat >/dev/null; echo edited > file.txt; echo ready; sleep 60")
	h := launch(t, cfg)
	task := h.start(t, "keep partial edits")
	deadline := time.Now().Add(5 * time.Second)
	for {
		var page Log
		h.request(t, "GET", "/v1/tasks/"+task.ID+"/log", nil, &page)
		if strings.Contains(page.Text, "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not edit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.request(t, "POST", "/v1/tasks/"+task.ID+"/cancel", nil, nil)
	h.wait(t, task.ID, "canceled")
	if got := testGit(t, repo, "show", task.Branch+":file.txt"); got != "edited" {
		t.Fatal(got)
	}
}

func TestWorkDirLock(t *testing.T) {
	cfg, _ := runnerConfig(t, "cat >/dev/null")
	if err := os.MkdirAll(cfg.WorkDir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(cfg.WorkDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = Serve(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "already using work_dir") {
		t.Fatal(err)
	}
}
