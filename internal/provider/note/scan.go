package note

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
)

type entry struct {
	name string
	info fs.FileInfo
}

var errStopWalk = errors.New("stop walk")

func (p *provider) walk(ctx context.Context, folder string, recursive bool, visit func(entry) error) error {
	resolved, err := p.resolve(folder, false)
	if err != nil {
		return err
	}
	count := 0
	ancestors := map[string]bool{}
	var walk func(string, string, int) error
	walk = func(actual, display string, depth int) error {
		if ancestors[actual] {
			return nil
		}
		ancestors[actual] = true
		defer delete(ancestors, actual)
		if depth > 128 {
			return fmt.Errorf("vault directory depth exceeds limit")
		}
		dir, err := p.directory(actual, false)
		if err != nil {
			return err
		}
		defer dir.Close()
		f, err := dir.Open(".")
		if err != nil {
			return errMissing
		}
		defer f.Close()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			items, readErr := f.ReadDir(256)
			for _, item := range items {
				count++
				if count > 100000 {
					return fmt.Errorf("vault scan exceeds 100000 entries")
				}
				name := path.Join(display, item.Name())
				real := path.Join(actual, item.Name())
				if p.validPath(name, false) != nil || p.validPath(real, false) != nil {
					continue
				}
				st, err := dir.Lstat(item.Name())
				if err != nil {
					continue
				}
				if st.Mode()&fs.ModeSymlink != 0 {
					real, err = p.resolve(real, false)
					if err != nil {
						continue
					}
					statPath := real
					if statPath == "" {
						statPath = "."
					}
					st, err = p.root.Lstat(statPath)
					if err != nil || st.Mode()&fs.ModeSymlink != 0 {
						continue
					}
				}
				ext := path.Ext(name)
				if !st.IsDir() && (!st.Mode().IsRegular() || (ext != ".md" && ext != ".canvas" && ext != ".base")) {
					continue
				}
				if err := visit(entry{name, st}); err != nil {
					return err
				}
				if recursive && st.IsDir() {
					if err := walk(real, name, depth+1); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return fmt.Errorf("cannot list vault directory")
			}
		}
	}
	err = walk(resolved, folder, 0)
	if errors.Is(err, errStopWalk) {
		return nil
	}
	return err
}
