package note

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

func (p *provider) trashName(base string) (string, error) {
	// Only this operation may access .trash; symlink aliases are never accepted.
	st, err := p.root.Lstat(".trash")
	if err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("trash directory unavailable")
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("trash directory unavailable")
	}
	for i := 0; i < 10000; i++ {
		candidate := path.Join(".trash", base)
		if i > 0 {
			candidate = path.Join(".trash", strings.TrimSuffix(base, ".md")+fmt.Sprintf(".%d.md", i))
		}
		if _, e := p.root.Lstat(candidate); errors.Is(e, fs.ErrNotExist) {
			return candidate, nil
		} else if e != nil {
			return "", fmt.Errorf("trash directory unavailable")
		}
	}
	return "", fmt.Errorf("trash name limit reached")
}

func (p *provider) moveToTrash(ctx context.Context, source *os.Root, base string, d writePlan) error {
	if err := p.root.Mkdir(".trash", 0775); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("cannot create trash directory")
	}
	trash, err := p.directory(".trash", false)
	if err != nil {
		return fmt.Errorf("trash directory unavailable")
	}
	defer trash.Close()
	return p.trashNote(ctx, source, trash, base, d)
}
