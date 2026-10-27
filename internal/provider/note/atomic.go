package note

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/gkdis6/opsgate/internal/tool"
)

func (p *provider) apply(ctx context.Context, plan tool.Plan) (string, error) {
	d, ok := plan.Data.(writePlan)
	if !ok || !p.cfg.AllowWrite {
		return "", tool.Denied("invalid write plan")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := p.resolve(d.name, d.action == "create")
	if err != nil || resolved != d.resolved {
		return "", fmt.Errorf("note changed since it was approved")
	}
	dir, err := p.directory(path.Dir(resolved), d.action == "create")
	if err != nil {
		return "", err
	}
	defer dir.Close()
	base := path.Base(resolved)
	if d.action == "create" {
		if _, err := dir.Lstat(base); !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("note changed since it was approved")
		}
	} else {
		current, err := p.loadFrom(dir, base, resolved)
		if err != nil || sha256.Sum256([]byte(current.text)) != d.hash || current.info.Size() != d.size || !current.info.ModTime().Equal(d.mtime) || !os.SameFile(current.info, d.info) {
			return "", fmt.Errorf("note changed since it was approved")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d.action == "trash" {
		if err := p.moveToTrash(ctx, dir, base, d); err != nil {
			return "", err
		}
	} else {
		mode := os.FileMode(0664)
		if d.info != nil {
			mode = d.info.Mode().Perm()
		}
		if err := atomicWrite(ctx, dir, base, d.text, mode, d.action == "create"); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s note %s", d.action, d.name), nil
}

func atomicWrite(ctx context.Context, dir *os.Root, base, text string, mode os.FileMode, create bool) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("cannot allocate temporary note")
	}
	temp := fmt.Sprintf(".opsgate-%x", random)
	f, err := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("cannot create temporary note")
	}
	defer dir.Remove(temp)
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
	}()
	if _, err = f.WriteString(text); err != nil {
		return fmt.Errorf("cannot write temporary note")
	}
	if err = f.Chmod(mode); err != nil {
		return fmt.Errorf("cannot set note permissions")
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("cannot sync temporary note")
	}
	err = f.Close()
	closed = true
	if err != nil {
		return fmt.Errorf("cannot close temporary note")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if create {
		// Link publishes the fully synced inode atomically and fails if the target
		// appeared. Root.Rename would silently overwrite a racing creator.
		err = dir.Link(temp, base)
	} else {
		err = dir.Rename(temp, base)
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("note changed since it was approved")
	}
	if err != nil {
		return fmt.Errorf("cannot publish note")
	}
	return syncDir(dir)
}

func syncDir(dir *os.Root) error {
	f, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("cannot open directory for sync")
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("cannot sync note directory")
	}
	return nil
}
