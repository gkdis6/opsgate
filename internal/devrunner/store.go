package devrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

var taskID = regexp.MustCompile(`^[a-z2-7]{20}$`)

func (r *runner) load() error {
	f, err := r.root.Open("tasks.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 64<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r.tasks); err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return fmt.Errorf("invalid task store")
	}
	seen := map[string]bool{}
	for _, t := range r.tasks {
		if t == nil || !taskID.MatchString(t.ID) || seen[t.ID] || t.Branch != "opsgate/"+t.ID {
			return fmt.Errorf("invalid task store")
		}
		seen[t.ID] = true
		switch t.State {
		case "queued", "running":
			t.State, t.Finished = "interrupted", time.Now().UTC()
		case "succeeded", "failed", "canceled", "timed_out", "interrupted":
		default:
			return fmt.Errorf("invalid task state")
		}
	}
	if err := r.prune(r.cfg.MaxTasks); err != nil {
		return err
	}
	return r.save()
}

// Caller holds mu. Persistence failure stops dispatch and rejects new writes.
func (r *runner) save() error {
	fail := func(err error) error {
		r.storageErr = fmt.Errorf("persist tasks: %w", err)
		r.stop()
		return r.storageErr
	}
	b, err := json.Marshal(r.tasks)
	if err != nil {
		return fail(err)
	}
	f, err := r.root.OpenFile("tasks.json.tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		if err = r.root.Remove("tasks.json.tmp"); err == nil {
			f, err = r.root.OpenFile("tasks.json.tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		}
	}
	if err != nil {
		return fail(err)
	}
	defer r.root.Remove("tasks.json.tmp")
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = r.root.Rename("tasks.json.tmp", "tasks.json")
	}
	if err == nil {
		var d *os.File
		d, err = r.root.Open(".")
		if err == nil {
			err = d.Sync()
			d.Close()
		}
	}
	if err != nil {
		return fail(err)
	}
	return nil
}

func (r *runner) prune(keep int) error {
	for len(r.tasks) > keep {
		idx := -1
		for i, t := range r.tasks {
			if finished(t.State) && (idx < 0 || t.Created.Before(r.tasks[idx].Created)) {
				idx = i
			}
		}
		if idx < 0 {
			return fmt.Errorf("runner task capacity reached")
		}
		t := r.tasks[idx]
		p, ok := r.project(t.Project)
		if !ok {
			return fmt.Errorf("retained task project is no longer configured; restore it before pruning")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, statErr := r.root.Stat("tasks/" + t.ID + "/worktree")
		if statErr == nil {
			_, _, err := git(ctx, p.Path, MaxReadBytes, "worktree", "remove", "--force", r.dir(t.ID)+"/worktree")
			if err != nil {
				cancel()
				return fmt.Errorf("remove retained worktree: %w", err)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			cancel()
			return statErr
		}
		cancel()
		if err := r.root.RemoveAll("tasks/" + t.ID); err != nil {
			return err
		}
		r.tasks = append(r.tasks[:idx], r.tasks[idx+1:]...)
	}
	return nil
}
