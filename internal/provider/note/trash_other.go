//go:build !linux

package note

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path"
)

func (p *provider) trashNote(ctx context.Context, source, trash *os.Root, base string, d writePlan) error {
	// Without a portable cross-Root no-clobber rename, retain a durable copy first.
	// This is weaker: a replacement between the final check and Remove can be lost.
	if err := atomicWrite(ctx, trash, path.Base(d.destination), d.text, d.info.Mode().Perm(), true); err != nil {
		return err
	}
	current, err := p.loadFrom(source, base, d.resolved)
	if err != nil || sha256.Sum256([]byte(current.text)) != d.hash || !os.SameFile(current.info, d.info) || !current.info.ModTime().Equal(d.mtime) {
		return fmt.Errorf("note changed since it was approved; approved copy retained in %s", d.destination)
	}
	if p.beforeTrashMove != nil {
		p.beforeTrashMove()
	}
	if err := source.Remove(base); err != nil {
		return fmt.Errorf("note retained; trash copy created at %s", d.destination)
	}
	return syncDir(source)
}
