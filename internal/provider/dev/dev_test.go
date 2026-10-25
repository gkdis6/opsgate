package dev

import (
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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/config"
	"github.com/gkdis6/opsgate/internal/devrunner"
	"github.com/gkdis6/opsgate/internal/tool"
)

type confirmFunc func(tool.ConfirmRequest) error

func (f confirmFunc) Confirm(_ context.Context, _ *mcp.CallToolRequest, r tool.ConfirmRequest) (string, error) {
	return "test", f(r)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeProvider(handler http.Handler) *provider {
	return &provider{client: &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Result(), nil
	})}}
}
func connect(t *testing.T, socket string, scopes []string, confirm tool.Confirmer, providers ...*provider) *mcp.ClientSession {
	t.Helper()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { al.Close() })
	env := &tool.Env{Audit: al, Timeout: 5 * time.Second, MaxOutputBytes: 1 << 20, Fallback: &tool.Caller{User: "operator", Transport: "stdio", Scopes: scopes}, Confirm: confirm}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	var registerErr error
	if len(providers) > 0 {
		registerErr = registerTools(s, env, providers[0])
	} else {
		registerErr = Register(s, env, config.Dev{RunnerSocket: socket})
	}
	if err := registerErr; err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}
func call(t *testing.T, cs *mcp.ClientSession, name string, args any, wantError bool) string {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s: %+v", name, result)
	}
	if len(result.Content) != 1 {
		t.Fatal(result)
	}
	return result.Content[0].(*mcp.TextContent).Text
}

func TestPlanAndApplyBinding(t *testing.T) {
	var mu sync.Mutex
	base := "main"
	starts := 0
	var got devrunner.StartRequest
	p := fakeProvider(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch req.URL.Path {
		case "/v1/info":
			json.NewEncoder(w).Encode(devrunner.Info{Projects: []devrunner.Project{{Name: "app", BaseRef: base}}, Agents: []string{"fake"}})
		case "/v1/tasks":
			starts++
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(devrunner.Task{ID: strings.Repeat("a", 20), Branch: "opsgate/" + strings.Repeat("a", 20)})
		default:
			t.Error(req.URL)
		}
	}))
	in := startInput{Project: "app", Agent: "fake", Prompt: "fix the bug\nthen test"}
	caller := tool.Caller{User: "operator"}
	plan, err := p.plan(context.Background(), caller, in)
	want := "Start fake on project app (base main) in a new branch; it may edit files and run commands inside its sandbox. Requirement:\n" + in.Prompt
	if err != nil || plan.Summary != want {
		t.Fatalf("%+v %v", plan, err)
	}
	for _, tc := range []struct {
		name string
		in   startInput
	}{
		{"project", startInput{Project: "other", Agent: "fake", Prompt: "ok"}},
		{"agent", startInput{Project: "app", Agent: "other", Prompt: "ok"}},
		{"empty", startInput{Project: "app", Agent: "fake"}},
		{"large", startInput{Project: "app", Agent: "fake", Prompt: strings.Repeat("x", devrunner.MaxPromptBytes+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.plan(context.Background(), caller, tc.in); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	changed := in
	changed.Prompt = "different"
	if _, err := p.apply(context.Background(), caller, changed, plan); err == nil {
		t.Fatal("changed arguments applied")
	}
	mu.Lock()
	base = "other"
	mu.Unlock()
	if _, err := p.apply(context.Background(), caller, in, plan); err == nil {
		t.Fatal("changed policy applied")
	}
	mu.Lock()
	base = "main"
	mu.Unlock()
	out, err := p.apply(context.Background(), caller, in, plan)
	if err != nil || !strings.Contains(out, "dev_task_status") || starts != 1 || got.Prompt != in.Prompt || got.RequestedBy != "operator" {
		t.Fatalf("%s %v %+v starts=%d", out, err, got, starts)
	}
}

func TestRegisterDownAndScopes(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing.sock")
	cs := connect(t, socket, []string{config.ScopeDev}, nil)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 7 {
		t.Fatal(len(listed.Tools))
	}
	for _, spec := range listed.Tools {
		if !strings.Contains(spec.Description, "untrusted") || !strings.Contains(spec.Description, "Nothing is merged") {
			t.Fatal(spec.Description)
		}
		readOnly := spec.Name != "dev_task_start" && spec.Name != "dev_task_cancel"
		if spec.Annotations.ReadOnlyHint != readOnly {
			t.Fatal(spec)
		}
	}
	if out := call(t, cs, "dev_projects", map[string]any{}, true); !strings.Contains(out, "unavailable") {
		t.Fatal(out)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"dev_task_status", map[string]any{"id": "../main"}},
		{"dev_task_diff", map[string]any{"id": "--help"}},
		{"dev_task_cancel", map[string]any{"id": "../main"}},
		{"dev_task_log", map[string]any{"id": strings.Repeat("a", 20), "offset": -1}},
		{"dev_task_list", map[string]any{"limit": 101}},
	} {
		call(t, cs, tc.name, tc.args, true)
	}
	unprivileged := connect(t, socket, []string{config.ScopeRead, config.ScopeWrite}, nil)
	for _, spec := range listed.Tools {
		args := map[string]any{"id": strings.Repeat("a", 20)}
		if spec.Name == "dev_projects" || spec.Name == "dev_task_list" {
			args = map[string]any{}
		}
		if spec.Name == "dev_task_start" {
			args = map[string]any{"project": "app", "agent": "fake", "prompt": "ok"}
		}
		if out := call(t, unprivileged, spec.Name, args, true); !strings.Contains(out, "missing scope dev:run") {
			t.Fatal(out)
		}
	}
}

func TestRunnerIntegration(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "og-dev-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "runner.sock")
	probe, err := net.Listen("unix", socket)
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox denies Unix socket creation")
	}
	if err != nil {
		t.Fatal(err)
	}
	probe.Close()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"-c", "user.name=Test", "-c", "user.email=test@localhost", "commit", "--allow-empty", "-m", "base"}} {
		argv := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "safe.directory=" + repo, "-C", repo}, args...)
		if b, err := exec.CommandContext(context.Background(), "/usr/bin/git", argv...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", b, err)
		}
	}
	agent := filepath.Join(dir, "agent")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\ncat > requirement.txt\necho changed > file.txt\necho done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroupId(fmt.Sprint(os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DevRunner{Socket: socket, SocketGroup: group.Name, WorkDir: filepath.Join(dir, "state"), Projects: []config.DevProject{{Name: "app", Path: repo, BaseRef: "main"}}, Agents: map[string]config.DevAgent{"fake": {Command: []string{agent}}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- devrunner.Serve(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	p, _ := newProvider(socket)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var info devrunner.Info
		if p.request(ctx, tool.Caller{User: "operator"}, "GET", "/v1/info", nil, &info) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runner did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	denied := connect(t, socket, []string{config.ScopeDev}, nil)
	call(t, denied, "dev_task_start", map[string]any{"project": "app", "agent": "fake", "prompt": "fix"}, true)
	confirmations := 0
	cs := connect(t, socket, []string{config.ScopeDev}, confirmFunc(func(r tool.ConfirmRequest) error {
		confirmations++
		if r.Summary != "Start fake on project app (base main) in a new branch; it may edit files and run commands inside its sandbox. Requirement:\nfix" {
			t.Error(r.Summary)
		}
		return nil
	}))
	call(t, cs, "dev_projects", map[string]any{}, false)
	out := call(t, cs, "dev_task_start", map[string]any{"project": "app", "agent": "fake", "prompt": "fix"}, false)
	if confirmations != 1 {
		t.Fatal(confirmations)
	}
	id := strings.Fields(out)[2]
	deadline = time.Now().Add(5 * time.Second)
	for {
		out = call(t, cs, "dev_task_status", map[string]any{"id": id}, false)
		var task devrunner.Task
		if err := json.Unmarshal([]byte(out), &task); err != nil {
			t.Fatal(err)
		}
		if task.State == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(task)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, name := range []string{"dev_task_log", "dev_task_diff", "dev_task_cancel"} {
		call(t, cs, name, map[string]any{"id": id}, false)
	}
	call(t, cs, "dev_task_list", map[string]any{}, false)
}

func TestClientBoundedResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"large", strings.Repeat("x", (2<<20)+1), 200},
		{"invalid", "{} {}", 200},
		{"error", "secret material", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fakeProvider(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			var out any
			if err := p.request(context.Background(), tool.Caller{User: "operator"}, "GET", "/v1/info", nil, &out); err == nil || strings.Contains(err.Error(), "secret material") {
				t.Fatal(err)
			}
		})
	}
}

func TestApprovedToolFlow(t *testing.T) {
	id := strings.Repeat("a", 20)
	starts := 0
	p := fakeProvider(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if users := req.Header.Values("X-Opsgate-User"); len(users) != 1 || users[0] != "operator" {
			t.Errorf("caller identity not forwarded: %q", users)
		}
		switch req.URL.Path {
		case "/v1/info":
			json.NewEncoder(w).Encode(devrunner.Info{Projects: []devrunner.Project{{Name: "app", BaseRef: "main"}}, Agents: []string{"fake"}})
		case "/v1/tasks":
			if req.Method == "POST" {
				starts++
				var in devrunner.StartRequest
				if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
					t.Error(err)
				}
				if in.RequestedBy != "operator" || in.Prompt != "fix" {
					t.Errorf("request %+v", in)
				}
				json.NewEncoder(w).Encode(devrunner.Task{ID: id, Branch: "opsgate/" + id})
			} else {
				json.NewEncoder(w).Encode([]devrunner.Task{{ID: id}})
			}
		case "/v1/tasks/" + id:
			json.NewEncoder(w).Encode(devrunner.Task{ID: id, State: "succeeded", Message: "done"})
		case "/v1/tasks/" + id + "/log":
			json.NewEncoder(w).Encode(devrunner.Log{Text: "done", NextOffset: 4})
		case "/v1/tasks/" + id + "/diff":
			json.NewEncoder(w).Encode(devrunner.Diff{Text: "+change"})
		case "/v1/tasks/" + id + "/cancel":
			json.NewEncoder(w).Encode(devrunner.Task{ID: id, State: "canceled"})
		default:
			t.Error(req.URL)
		}
	}))
	in := map[string]any{"project": "app", "agent": "fake", "prompt": "fix"}
	denied := connect(t, "", []string{config.ScopeDev}, nil, p)
	call(t, denied, "dev_task_start", in, true)
	if starts != 0 {
		t.Fatal("unapproved start")
	}
	confirmed := 0
	cs := connect(t, "", []string{config.ScopeDev}, confirmFunc(func(r tool.ConfirmRequest) error {
		confirmed++
		if !strings.HasSuffix(r.Summary, "Requirement:\nfix") {
			t.Error(r.Summary)
		}
		return nil
	}), p)
	call(t, cs, "dev_projects", map[string]any{}, false)
	out := call(t, cs, "dev_task_start", in, false)
	if starts != 1 || confirmed != 1 || !strings.Contains(out, id) {
		t.Fatalf("%s starts=%d approvals=%d", out, starts, confirmed)
	}
	for _, name := range []string{"dev_task_status", "dev_task_log", "dev_task_diff", "dev_task_cancel"} {
		call(t, cs, name, map[string]any{"id": id}, false)
	}
	call(t, cs, "dev_task_list", map[string]any{}, false)
}
