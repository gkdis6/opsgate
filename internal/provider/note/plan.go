package note

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

func (p *provider) planCreate(ctx context.Context, _ tool.Caller, in createInput) (tool.Plan, error) {
	if err := ctx.Err(); err != nil {
		return tool.Plan{}, err
	}
	if !p.cfg.AllowWrite {
		return tool.Plan{}, tool.Denied("vault writes disabled")
	}
	name, err := p.notePath(in.Path)
	if err != nil {
		if errors.Is(err, errMissing) {
			return tool.Plan{}, fmt.Errorf("cannot create note at a hidden, excluded or masked path: %w", err)
		}
		return tool.Plan{}, err
	}
	if len(in.Content) > maxContentBytes || len(in.Content) > p.cfg.MaxNoteBytes {
		return tool.Plan{}, fmt.Errorf("content exceeds note size limit")
	}
	resolved, err := p.resolve(name, true)
	if err != nil {
		if errors.Is(err, errMissing) {
			return tool.Plan{}, fmt.Errorf("cannot create note at a hidden, excluded or masked path: %w", err)
		}
		return tool.Plan{}, err
	}
	if _, err := p.root.Lstat(name); err == nil {
		return tool.Plan{}, fmt.Errorf("note already exists")
	}
	if _, err := p.root.Lstat(resolved); !errors.Is(err, fs.ErrNotExist) {
		return tool.Plan{}, fmt.Errorf("note already exists or unavailable")
	}
	d := writePlan{action: "create", name: name, resolved: resolved, text: in.Content}
	return preview(d, "", in.Content), nil
}

func (p *provider) existing(ctx context.Context, action, name string) (writePlan, string, error) {
	if err := ctx.Err(); err != nil {
		return writePlan{}, "", err
	}
	if !p.cfg.AllowWrite {
		return writePlan{}, "", tool.Denied("vault writes disabled")
	}
	name, err := p.notePath(name)
	if err != nil {
		return writePlan{}, "", err
	}
	n, err := p.load(name)
	if err != nil {
		return writePlan{}, "", err
	}
	return writePlan{action: action, name: name, resolved: n.resolved, hash: sha256.Sum256([]byte(n.text)), size: n.info.Size(), mtime: n.info.ModTime(), info: n.info}, n.text, nil
}

func (p *provider) planAppend(ctx context.Context, _ tool.Caller, in appendInput) (tool.Plan, error) {
	if len(in.Content) > maxContentBytes || len(in.Heading) > 1024 {
		return tool.Plan{}, fmt.Errorf("append input exceeds limit")
	}
	d, before, err := p.existing(ctx, "append", in.Path)
	if err != nil {
		return tool.Plan{}, err
	}
	at := len(before)
	if in.Heading != "" {
		at, err = sectionEnd(before, in.Heading)
		if err != nil {
			return tool.Plan{}, err
		}
	}
	newline := "\n"
	if strings.HasSuffix(before, "\r\n") {
		newline = "\r\n"
	}
	addition := strings.TrimRight(in.Content, "\r\n")
	prefix := before[:at]
	suffix := before[at:]
	if addition != "" {
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += newline
		}
		prefix += addition
		if suffix != "" {
			prefix += newline
		}
	}
	d.text = preserveEnding(prefix+suffix, before)
	if len(d.text) > p.cfg.MaxNoteBytes {
		return tool.Plan{}, fmt.Errorf("result exceeds max_note_bytes")
	}
	return preview(d, before, d.text), nil
}

func (p *provider) planReplace(ctx context.Context, _ tool.Caller, in replaceInput) (tool.Plan, error) {
	count := in.ExpectedCount
	if count == 0 {
		count = 1
	}
	if count < 1 || in.OldText == "" || len(in.OldText) > maxContentBytes || len(in.NewText) > maxContentBytes {
		return tool.Plan{}, fmt.Errorf("old_text must be nonempty and replacement/count within limits")
	}
	d, before, err := p.existing(ctx, "replace", in.Path)
	if err != nil {
		return tool.Plan{}, err
	}
	matches := replacementMatches(before, in.OldText)
	found := len(matches)
	if found != count {
		return tool.Plan{}, fmt.Errorf("replace count mismatch: expected %d, found %d", count, found)
	}
	// Check arithmetic before allocating the replacement.
	delta := int64(len(in.NewText) - len(in.OldText))
	size := int64(len(before)) + int64(found)*delta
	if size > int64(p.cfg.MaxNoteBytes)+2 {
		return tool.Plan{}, fmt.Errorf("result exceeds max_note_bytes")
	}
	var out strings.Builder
	cursor := 0
	for _, at := range matches {
		out.WriteString(before[cursor:at])
		out.WriteString(in.NewText)
		cursor = at + len(in.OldText)
	}
	out.WriteString(before[cursor:])
	d.text = preserveEnding(out.String(), before)
	if len(d.text) > p.cfg.MaxNoteBytes {
		return tool.Plan{}, fmt.Errorf("result exceeds max_note_bytes")
	}
	return preview(d, before, d.text), nil
}

// replacementMatches excludes matches touching secret ranges.
func replacementMatches(text, old string) []int {
	var matches []int
	find := func(start, end int) {
		for start < end {
			at := strings.Index(text[start:end], old)
			if at < 0 {
				break
			}
			matches = append(matches, start+at)
			start += at + len(old)
		}
	}
	start := 0
	for _, r := range redact.Ranges(text) {
		find(start, r.Start)
		start = r.End
	}
	find(start, len(text))
	return matches
}

func (p *provider) planTrash(ctx context.Context, _ tool.Caller, in trashInput) (tool.Plan, error) {
	d, before, err := p.existing(ctx, "trash", in.Path)
	if err != nil {
		return tool.Plan{}, err
	}
	dest, err := p.trashName(path.Base(d.name))
	if err != nil {
		return tool.Plan{}, err
	}
	d.destination = dest
	d.text = before
	return preview(d, before, ""), nil
}

func preserveEnding(text, before string) string {
	if strings.HasSuffix(before, "\r\n") {
		if strings.HasSuffix(text, "\r\n") {
			return text
		}
		return strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r") + "\r\n"
	}
	if strings.HasSuffix(before, "\n") {
		if strings.HasSuffix(text, "\n") {
			return text
		}
		return text + "\n"
	}
	return strings.TrimRight(text, "\r\n")
}
