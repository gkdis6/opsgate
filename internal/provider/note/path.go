package note

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/redact"
)

var errMissing = fs.ErrNotExist

func (p *provider) validPath(name string, rootOK bool) error {
	if name == "" && rootOK {
		return nil
	}
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("invalid vault-relative path")
	}
	if redact.String(name) != name {
		return errMissing
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == ".." {
			return fmt.Errorf("invalid vault-relative path")
		}
		if strings.HasPrefix(segment, ".") || strings.Contains(segment, ".sync-conflict-") {
			return errMissing
		}
	}
	for current := name; current != "."; current = path.Dir(current) {
		for _, pattern := range p.cfg.Exclude {
			if ok, _ := path.Match(pattern, current); ok {
				return errMissing
			}
		}
	}
	return nil
}

func (p *provider) notePath(name string) (string, error) {
	if err := p.validPath(name, false); err != nil {
		return "", err
	}
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	if err := p.validPath(name, false); err != nil {
		return "", err
	}
	return name, nil
}

// Resolve aliases before policy checks, then open each directory without following
// a replacement symlink. Root still enforces confinement during every operation.
func (p *provider) resolve(name string, missingOK bool) (string, error) {
	if err := p.validPath(name, true); err != nil {
		return "", err
	}
	if name == "" {
		return "", nil
	}
	for hops := 0; hops < 40; hops++ {
		parts := strings.Split(name, "/")
		restart := false
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			st, err := p.root.Lstat(prefix)
			if errors.Is(err, fs.ErrNotExist) && missingOK {
				return name, nil
			}
			if err != nil {
				return "", errMissing
			}
			if st.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := p.root.Readlink(prefix)
			if err != nil || path.IsAbs(target) {
				return "", errMissing
			}
			name = path.Clean(path.Join(path.Dir(prefix), target, path.Join(parts[i+1:]...)))
			if name == "." {
				name = ""
			}
			if err := p.validPath(name, true); err != nil {
				return "", errMissing
			}
			restart = true
			break
		}
		if !restart {
			return name, nil
		}
	}
	return "", errMissing
}

func (p *provider) directory(name string, create bool) (*os.Root, error) {
	current, err := p.root.OpenRoot(".")
	if err != nil {
		return nil, errMissing
	}
	if name == "" || name == "." {
		return current, nil
	}
	for _, segment := range strings.Split(name, "/") {
		st, e := current.Lstat(segment)
		if errors.Is(e, fs.ErrNotExist) && create {
			e = current.Mkdir(segment, 0775)
			if e == nil || errors.Is(e, fs.ErrExist) {
				st, e = current.Lstat(segment)
			}
		}
		if e != nil || st == nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, errMissing
		}
		next, e := current.OpenRoot(segment)
		if e != nil {
			current.Close()
			return nil, errMissing
		}
		pinned, e := next.Stat(".")
		current.Close()
		if e != nil || !os.SameFile(st, pinned) {
			next.Close()
			return nil, errMissing
		}
		current = next
	}
	return current, nil
}

type note struct {
	text     string
	info     fs.FileInfo
	resolved string
}

func (p *provider) load(name string) (note, error) {
	resolved, err := p.resolve(name, false)
	if err != nil {
		return note{}, err
	}
	if !strings.HasSuffix(resolved, ".md") {
		return note{}, errMissing
	}
	dir, err := p.directory(path.Dir(resolved), false)
	if err != nil {
		return note{}, err
	}
	defer dir.Close()
	return p.loadFrom(dir, path.Base(resolved), resolved)
}

func (p *provider) loadFrom(dir *os.Root, base, resolved string) (note, error) {
	f, err := dir.OpenFile(base, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return note{}, errMissing
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return note{}, errMissing
	}
	if st.Size() > int64(p.cfg.MaxNoteBytes) {
		return note{}, fmt.Errorf("note exceeds max_note_bytes")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(p.cfg.MaxNoteBytes)+1))
	if err != nil {
		return note{}, fmt.Errorf("cannot read note")
	}
	if len(data) > p.cfg.MaxNoteBytes {
		return note{}, fmt.Errorf("note exceeds max_note_bytes")
	}
	after, err := f.Stat()
	if err != nil || st.Size() != after.Size() || int64(len(data)) != after.Size() || !st.ModTime().Equal(after.ModTime()) {
		return note{}, fmt.Errorf("note changed while reading")
	}
	return note{string(data), after, resolved}, nil
}
