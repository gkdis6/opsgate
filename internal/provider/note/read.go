package note

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

type pathInput struct {
	Path string `json:"path" jsonschema:"Vault-relative note path; .md may be omitted; maximum 1024 bytes."`
}
type readInput struct {
	Path       string `json:"path" jsonschema:"Vault-relative note path; .md may be omitted; maximum 1024 bytes."`
	OffsetLine int    `json:"offset_line,omitempty" jsonschema:"First line, 1-based; default 1."`
	MaxLines   int    `json:"max_lines,omitempty" jsonschema:"Maximum lines; default 400, maximum 2000."`
}
type listInput struct {
	Folder    string `json:"folder,omitempty" jsonschema:"Vault-relative folder; empty means root."`
	Recursive bool   `json:"recursive,omitempty"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Default 200, maximum 1000."`
}
type recentInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"Default 20, maximum 100."`
	Days  int `json:"days,omitempty" jsonschema:"Default 7, maximum 365."`
}

func noteLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func (p *provider) read(ctx context.Context, _ tool.Caller, in readInput) (string, error) {
	name, err := p.notePath(in.Path)
	if err != nil {
		return "", err
	}
	max, err := limit(in.MaxLines, 400, 2000)
	if err != nil {
		return "", err
	}
	offset := in.OffsetLine
	if offset == 0 {
		offset = 1
	}
	if offset < 1 {
		return "", fmt.Errorf("offset_line must be positive")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	n, err := p.load(name)
	if err != nil {
		return "", err
	}
	lines := noteLines(n.text)
	if offset > len(lines)+1 {
		return "", fmt.Errorf("offset_line exceeds total lines")
	}
	start := offset - 1
	end := start + min(max, len(lines)-start)
	byteStart := 0
	for _, line := range lines[:start] {
		byteStart += len(line) + 1
	}
	byteStart = min(byteStart, len(n.text))
	text := strings.Join(lines[start:end], "\n")
	if end > start && (end < len(lines) || strings.HasSuffix(n.text, "\n")) {
		text += "\n"
	}
	text = redact.Fragment(n.text, redact.Ranges(n.text), byteStart, byteStart+len(text))
	lineRange := fmt.Sprintf("%d-%d", offset, end)
	if start == end {
		lineRange = "none"
	}
	return fmt.Sprintf("path: %s\nsize: %d\nmodified: %s\nlines: %s (total %d)\n\n%s", name, n.info.Size(), n.info.ModTime().UTC().Format(time.RFC3339Nano), lineRange, len(lines), text), nil
}

func (p *provider) list(ctx context.Context, _ tool.Caller, in listInput) (string, error) {
	max, err := limit(in.Limit, 200, 1000)
	if err != nil {
		return "", err
	}
	var entries []entry
	err = p.walk(ctx, in.Folder, in.Recursive, func(e entry) error { entries = append(entries, e); return nil })
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].info.IsDir() != entries[j].info.IsDir() {
			return entries[i].info.IsDir()
		}
		return entries[i].name < entries[j].name
	})
	var out strings.Builder
	for _, e := range entries[:min(max, len(entries))] {
		if e.info.IsDir() {
			fmt.Fprintf(&out, "folder %s/\n", e.name)
		} else {
			fmt.Fprintf(&out, "%s\t%d bytes\t%s\n", e.name, e.info.Size(), e.info.ModTime().UTC().Format(time.RFC3339Nano))
		}
	}
	if len(entries) > max {
		fmt.Fprintf(&out, "truncated: %d more entries\n", len(entries)-max)
	}
	return out.String(), nil
}

func (p *provider) recent(ctx context.Context, _ tool.Caller, in recentInput) (string, error) {
	max, err := limit(in.Limit, 20, 100)
	if err != nil {
		return "", err
	}
	days, err := limit(in.Days, 7, 365)
	if err != nil {
		return "", err
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var entries []entry
	err = p.walk(ctx, "", true, func(e entry) error {
		if !e.info.IsDir() && path.Ext(e.name) == ".md" && !e.info.ModTime().Before(cutoff) {
			entries = append(entries, e)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].info.ModTime().Equal(entries[j].info.ModTime()) {
			return entries[i].name < entries[j].name
		}
		return entries[i].info.ModTime().After(entries[j].info.ModTime())
	})
	var out strings.Builder
	for _, e := range entries[:min(max, len(entries))] {
		fmt.Fprintf(&out, "%s\t%d bytes\t%s\n", e.name, e.info.Size(), e.info.ModTime().UTC().Format(time.RFC3339Nano))
	}
	if len(entries) > max {
		fmt.Fprintln(&out, "truncated")
	}
	return out.String(), nil
}
