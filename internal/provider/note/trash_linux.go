package note

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

func (p *provider) trashNote(ctx context.Context, source, trash *os.Root, base string, d writePlan) error {
	// Open through the pinned Roots; only single-component names reach renameat2.
	sourceDir, err := source.Open(".")
	if err != nil {
		return fmt.Errorf("source directory unavailable")
	}
	defer sourceDir.Close()
	trashDir, err := trash.Open(".")
	if err != nil {
		return fmt.Errorf("trash directory unavailable")
	}
	defer trashDir.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.beforeTrashMove != nil {
		p.beforeTrashMove()
	}
	destination := path.Base(d.destination)
	if err := unix.Renameat2(int(sourceDir.Fd()), base, int(trashDir.Fd()), destination, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("note changed since it was approved; trash destination already exists")
		}
		return fmt.Errorf("cannot move note to trash")
	}
	// Validate what was moved, so a racing replacement is preserved rather than unlinked.
	current, err := p.loadFrom(trash, destination, d.destination)
	changed := err != nil || sha256.Sum256([]byte(current.text)) != d.hash || current.info.Size() != d.size || !os.SameFile(current.info, d.info) || !current.info.ModTime().Equal(d.mtime)
	syncErr := errors.Join(syncTrashNote(trash, destination), syncDir(trash), syncDir(source))
	if changed {
		return errors.Join(fmt.Errorf("note changed since it was approved; it was moved to %s, restore it if needed", d.destination), syncErr)
	}
	if syncErr != nil {
		return fmt.Errorf("note moved to %s; %w", d.destination, syncErr)
	}
	return nil
}

func syncTrashNote(trash *os.Root, base string) error {
	f, err := trash.OpenFile(base, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("cannot sync trashed note")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("cannot sync trashed note")
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("cannot sync trashed note")
	}
	return nil
}
