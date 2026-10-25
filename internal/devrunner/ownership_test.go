package devrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gkdis6/opsgate/internal/config"
)

func ownershipRunner(t *testing.T) *runner {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	return &runner{cfg: config.DevRunner{Admins: []string{"admin"}, MaxTasks: 100}, root: root, ctx: ctx, stop: stop, cancels: map[string]context.CancelFunc{}, wake: make(chan struct{}, 1)}
}

func ownershipRequest(r *runner, method, path string, users ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, http.NoBody)
	for _, user := range users {
		req.Header.Add("X-Opsgate-User", user)
	}
	out := httptest.NewRecorder()
	r.handler().ServeHTTP(out, req)
	return out
}

func TestTaskOwnership(t *testing.T) {
	id := strings.Repeat("a", 20)
	for _, endpoint := range []struct{ method, suffix string }{{"GET", ""}, {"GET", "/log"}, {"GET", "/diff"}, {"POST", "/cancel"}} {
		for _, caller := range []struct {
			user   string
			status int
		}{{"owner", 200}, {"other", 404}, {"admin", 200}} {
			t.Run(endpoint.method+endpoint.suffix+"/"+caller.user, func(t *testing.T) {
				r := ownershipRunner(t)
				r.tasks = []*Task{{ID: id, owner: "owner", RequestedBy: "owner", State: "queued"}}
				canceled := false
				r.cancels[id] = func() { canceled = true }
				out := ownershipRequest(r, endpoint.method, "/v1/tasks/"+id+endpoint.suffix, caller.user)
				if out.Code != caller.status {
					t.Fatalf("status %d: %s", out.Code, out.Body)
				}
				wantCancel := endpoint.method == "POST" && caller.status == 200
				if canceled != wantCancel || (r.tasks[0].State == "canceled") != wantCancel {
					t.Fatal("unexpected cancellation")
				}
				if caller.status == 404 {
					missing := ownershipRequest(r, endpoint.method, "/v1/tasks/"+strings.Repeat("b", 20)+endpoint.suffix, caller.user)
					if out.Body.String() != missing.Body.String() {
						t.Fatal("foreign task disclosed")
					}
				}
			})
		}
	}
}

func TestListOwnership(t *testing.T) {
	for _, tc := range []struct {
		user string
		ids  []string
	}{
		{"owner", []string{"a"}}, {"other", []string{"c", "b"}}, {"admin", []string{"c", "b", "a"}}, {"unknown", []string{}},
	} {
		t.Run(tc.user, func(t *testing.T) {
			r := ownershipRunner(t)
			for i, user := range []string{"owner", "other", "other"} {
				r.tasks = append(r.tasks, &Task{ID: string(rune('a' + i)), owner: user, Message: "private"})
			}
			out := ownershipRequest(r, "GET", "/v1/tasks", tc.user)
			var tasks []Task
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &tasks) != nil || len(tasks) != len(tc.ids) {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
			for i, task := range tasks {
				if task.ID != tc.ids[i] || task.Message != "" {
					t.Fatalf("unexpected task %+v", task)
				}
			}
			// Filtering happens before limiting, including when newer tasks belong to others.
			page := ownershipRequest(r, "GET", "/v1/tasks?limit=1", tc.user)
			if err := json.Unmarshal(page.Body.Bytes(), &tasks); err != nil {
				t.Fatal(err)
			}
			if len(tc.ids) > 0 && (len(tasks) != 1 || tasks[0].ID != tc.ids[0]) {
				t.Fatalf("bad page: %s", page.Body)
			}
		})
	}
}

func TestCallerRequired(t *testing.T) {
	for _, users := range [][]string{nil, {""}, {"owner", "admin"}, {" owner"}, {"owner "}, {"a\tb"}, {"a\nadmin"}, {"\xff"}, {strings.Repeat("a", 257)}} {
		for _, endpoint := range []struct{ method, path string }{{"GET", "/v1/tasks"}, {"GET", "/v1/tasks/" + strings.Repeat("a", 20)}, {"GET", "/v1/tasks/" + strings.Repeat("a", 20) + "/log"}, {"GET", "/v1/tasks/" + strings.Repeat("a", 20) + "/diff"}, {"POST", "/v1/tasks/" + strings.Repeat("a", 20) + "/cancel"}, {"POST", "/v1/tasks"}} {
			r := ownershipRunner(t)
			if out := ownershipRequest(r, endpoint.method, endpoint.path, users...); out.Code != 401 {
				t.Fatalf("%q %s: %d", users, endpoint.path, out.Code)
			}
		}
	}
}

func TestOwnerPersistence(t *testing.T) {
	r := ownershipRunner(t)
	// Both subjects have the same redacted display value; ownership must stay distinct.
	owner := "ghp_aaaaaaaaaaaa"
	other := "ghp_bbbbbbbbbbbb"
	id := strings.Repeat("a", 20)
	r.tasks = []*Task{{ID: id, Branch: "opsgate/" + id, owner: owner, RequestedBy: "[REDACTED]", State: "succeeded"}}
	if err := r.save(); err != nil {
		t.Fatal(err)
	}
	r.tasks = nil
	if err := r.load(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user   string
		status int
	}{{owner, 200}, {other, 404}, {"admin", 200}} {
		out := ownershipRequest(r, "GET", "/v1/tasks/"+id, tc.user)
		if out.Code != tc.status || strings.Contains(out.Body.String(), owner) || strings.Contains(out.Body.String(), `"owner"`) {
			t.Fatalf("%d %s", out.Code, out.Body)
		}
	}
}

func TestLegacyOwnership(t *testing.T) {
	for _, subject := range []string{"operator", "[REDACTED]", ""} {
		t.Run(subject, func(t *testing.T) {
			r := ownershipRunner(t)
			id := strings.Repeat("a", 20)
			data, err := json.Marshal([]Task{{ID: id, Branch: "opsgate/" + id, RequestedBy: subject, State: "succeeded"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.root.WriteFile("tasks.json", data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := r.load(); err != nil {
				t.Fatal(err)
			}
			user, want := subject, 200
			if subject != "operator" {
				user, want = "[REDACTED]", 404
			}
			if out := ownershipRequest(r, "GET", "/v1/tasks/"+id, user); out.Code != want {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
			if out := ownershipRequest(r, "GET", "/v1/tasks/"+id, "admin"); out.Code != 200 {
				t.Fatal(out.Code)
			}
		})
	}
}

func TestStartBindsOwnerToCaller(t *testing.T) {
	for _, claimed := range []string{"", "owner", "other"} {
		t.Run(claimed, func(t *testing.T) {
			r := ownershipRunner(t)
			r.cfg.Projects = []config.DevProject{{Name: "app", BaseRef: "HEAD"}}
			r.cfg.Agents = map[string]config.DevAgent{"fake": {}}
			if err := r.root.Mkdir("tasks", 0700); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(StartRequest{Project: "app", Agent: "fake", Prompt: "fix", RequestedBy: claimed})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/v1/tasks", strings.NewReader(string(body)))
			req.Header.Set("X-Opsgate-User", "owner")
			out := httptest.NewRecorder()
			r.handler().ServeHTTP(out, req)
			if claimed == "other" {
				if out.Code != 400 || len(r.tasks) != 0 {
					t.Fatalf("forged attribution accepted: %d %s", out.Code, out.Body)
				}
				return
			}
			if out.Code != 202 || len(r.tasks) != 1 || r.tasks[0].owner != "owner" || r.tasks[0].RequestedBy != "owner" {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
			r.tasks = nil
			if err := r.load(); err != nil {
				t.Fatal(err)
			}
			if r.tasks[0].owner != "owner" {
				t.Fatal("owner not persisted")
			}
		})
	}
}
