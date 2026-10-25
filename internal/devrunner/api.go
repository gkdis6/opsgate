package devrunner

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/redact"
)

func (r *runner) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", r.info)
	mux.HandleFunc("POST /v1/tasks", r.start)
	mux.HandleFunc("GET /v1/tasks", r.list)
	mux.HandleFunc("GET /v1/tasks/{id}", r.status)
	mux.HandleFunc("GET /v1/tasks/{id}/log", r.log)
	mux.HandleFunc("GET /v1/tasks/{id}/diff", r.diff)
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", r.cancel)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if req.ProtoMajor != 1 || (req.ContentLength != 0 || len(req.TransferEncoding) > 0) && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
			http.Error(w, "invalid request", 400)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		mux.ServeHTTP(w, req.WithContext(ctx))
	})
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func bad(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func (r *runner) info(w http.ResponseWriter, req *http.Request) {
	if req.URL.RawQuery != "" {
		bad(w, 400, "unexpected query")
		return
	}
	info := Info{Timeout: r.cfg.Timeout, MaxConcurrent: r.cfg.MaxConcurrent, MaxTasks: r.cfg.MaxTasks, MaxPromptBytes: MaxPromptBytes, Projects: []Project{}, Agents: []string{}}
	for _, p := range r.cfg.Projects {
		info.Projects = append(info.Projects, Project{p.Name, p.BaseRef})
	}
	for a := range r.cfg.Agents {
		info.Agents = append(info.Agents, a)
	}
	slices.Sort(info.Agents)
	reply(w, 200, info)
}
func (r *runner) start(w http.ResponseWriter, req *http.Request) {
	if req.URL.RawQuery != "" {
		bad(w, 400, "unexpected query")
		return
	}
	var in StartRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		bad(w, 400, "invalid task JSON")
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		bad(w, 400, "expected one JSON object")
		return
	}
	p, ok := r.project(in.Project)
	_, agentOK := r.cfg.Agents[in.Agent]
	if !ok || !agentOK || len(in.Prompt) < 1 || len(in.Prompt) > MaxPromptBytes || !utf8.ValidString(in.Prompt) || strings.ContainsRune(in.Prompt, 0) || len(in.RequestedBy) > 256 || !utf8.ValidString(in.RequestedBy) {
		bad(w, 400, "invalid project, agent, prompt or requested_by")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx.Err() != nil || r.storageErr != nil {
		bad(w, 503, "runner unavailable")
		return
	}
	if err := r.prune(r.cfg.MaxTasks - 1); err != nil {
		bad(w, 503, "runner capacity or retention unavailable")
		return
	}
	// Persist pruning before accepting anything new, including disk failures.
	if err := r.save(); err != nil {
		bad(w, 503, "task persistence unavailable")
		return
	}
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		bad(w, 503, "task ID unavailable")
		return
	}
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
	if r.find(id) != nil {
		bad(w, 503, "task ID collision")
		return
	}
	dir := "tasks/" + id
	if err := r.root.Mkdir(dir, 0700); err != nil {
		bad(w, 503, "cannot create task directory")
		return
	}
	f, err := r.root.OpenFile(dir+"/prompt.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, err = f.WriteString(in.Prompt)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = r.root.RemoveAll(dir)
		bad(w, 503, "cannot save task prompt")
		return
	}
	t := &Task{ID: id, Project: p.Name, Agent: in.Agent, Branch: "opsgate/" + id, BaseRef: p.BaseRef, RequestedBy: redact.String(in.RequestedBy), State: "queued", Created: time.Now().UTC()}
	r.tasks = append(r.tasks, t)
	if err := r.save(); err != nil {
		bad(w, 503, "cannot persist task")
		return
	}
	r.signal()
	reply(w, 202, *t)
}
func number(req *http.Request, key string, fallback, maximum int64) (int64, error) {
	values := req.URL.Query()[key]
	if len(values) == 0 {
		return fallback, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, fmt.Errorf("invalid number")
	}
	n, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || n < 0 || n > maximum {
		return 0, fmt.Errorf("invalid number")
	}
	return n, nil
}
func queryOnly(req *http.Request, keys ...string) bool {
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return false
	}
	for key := range q {
		if !slices.Contains(keys, key) {
			return false
		}
	}
	return true
}

func (r *runner) list(w http.ResponseWriter, req *http.Request) {
	limit, err := number(req, "limit", 20, 100)
	if err != nil || limit < 1 || !queryOnly(req, "limit") {
		bad(w, 400, "invalid list limit")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Task, 0, min(int(limit), len(r.tasks)))
	for i := len(r.tasks) - 1; i >= 0 && len(out) < int(limit); i-- {
		t := *r.tasks[i]
		t.Message = ""
		out = append(out, t)
	}
	reply(w, 200, out)
}
func (r *runner) get(w http.ResponseWriter, req *http.Request) (Task, bool) {
	id := req.PathValue("id")
	if !taskID.MatchString(id) {
		bad(w, 400, "invalid task ID")
		return Task{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.find(id)
	if t == nil {
		bad(w, 404, "task not found")
		return Task{}, false
	}
	return *t, true
}
func (r *runner) status(w http.ResponseWriter, req *http.Request) {
	if !queryOnly(req) {
		bad(w, 400, "unexpected query")
		return
	}
	if t, ok := r.get(w, req); ok {
		reply(w, 200, t)
	}
}
func (r *runner) cancel(w http.ResponseWriter, req *http.Request) {
	if !queryOnly(req) {
		bad(w, 400, "unexpected query")
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 1))
	if err != nil || len(b) != 0 {
		bad(w, 400, "cancel takes no body")
		return
	}
	t, ok := r.get(w, req)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.find(t.ID)
	if current == nil {
		bad(w, 404, "task not found")
		return
	}
	if current.State == "queued" {
		current.State = "canceled"
		current.Finished = time.Now().UTC()
	}
	if cancel := r.cancels[t.ID]; cancel != nil {
		cancel()
	}
	if err := r.save(); err != nil {
		bad(w, 503, "task persistence unavailable")
		return
	}
	reply(w, 200, *current)
}
func (r *runner) log(w http.ResponseWriter, req *http.Request) {
	offset, e1 := number(req, "offset", 0, maxLogBytes)
	limit, e2 := number(req, "limit", 64<<10, MaxReadBytes)
	if e1 != nil || e2 != nil || limit < 1 || !queryOnly(req, "offset", "limit") {
		bad(w, 400, "invalid log page")
		return
	}
	t, ok := r.get(w, req)
	if !ok {
		return
	}
	b, err := readRegular(r.root, "tasks/"+t.ID+"/log.txt", offset, int(limit)+1)
	if os.IsNotExist(err) {
		reply(w, 200, Log{NextOffset: offset})
		return
	}
	if err != nil {
		bad(w, 503, "task log unavailable")
		return
	}
	truncated := len(b) > int(limit)
	if truncated {
		b = b[:limit]
	}
	reply(w, 200, Log{Text: redactedText(b), NextOffset: offset + int64(len(b)), Truncated: truncated})
}
func (r *runner) diff(w http.ResponseWriter, req *http.Request) {
	if !queryOnly(req) {
		bad(w, 400, "unexpected query")
		return
	}
	// Retention cannot remove this worktree while git reads it.
	r.mu.Lock()
	defer r.mu.Unlock()
	id := req.PathValue("id")
	if !taskID.MatchString(id) {
		bad(w, 400, "invalid task ID")
		return
	}
	t := r.find(id)
	if t == nil {
		bad(w, 404, "task not found")
		return
	}
	if t.BaseCommit == "" {
		reply(w, 200, Diff{})
		return
	}
	out, cut, err := git(req.Context(), r.dir(t.ID)+"/worktree", MaxReadBytes, "diff", "--no-ext-diff", "--no-textconv", "--stat", "--patch", t.BaseCommit, "--")
	if err != nil {
		bad(w, 503, "task diff unavailable")
		return
	}
	reply(w, 200, Diff{Text: redactedText([]byte(out)), Truncated: cut})
}

func redactedText(b []byte) string {
	s := redact.String(strings.ToValidUTF8(string(b), "�"))
	if len(s) > MaxReadBytes {
		s = s[:MaxReadBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
