package obsidian

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/tool"
)

type searchInput struct {
	Query         string `json:"query" jsonschema:"Nonempty literal substring; maximum 4096 bytes."`
	Folder        string `json:"folder,omitempty" jsonschema:"Vault-relative folder; empty means root."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Default 20, maximum 100."`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Tag           string `json:"tag,omitempty" jsonschema:"Optional tag filter, with or without #; includes nested tags."`
}

func (p *provider) search(ctx context.Context, _ tool.Caller, in searchInput) (string, error) {
	max, err := limit(in.Limit, 20, 100)
	if err != nil {
		return "", err
	}
	if in.Query == "" || len(in.Query) > 4096 || !utf8.ValidString(in.Query) {
		return "", fmt.Errorf("query must contain 1-4096 UTF-8 bytes")
	}
	tag, err := cleanTag(in.Tag)
	if err != nil {
		return "", err
	}
	query := in.Query
	if !in.CaseSensitive {
		query = strings.ToLower(query)
	}
	hits, scanned := 0, 0
	var out strings.Builder
	emit := func(name string, line int, text string) {
		hits++
		fmt.Fprintf(&out, "%s:%d: %s\n", name, line, snippet(text, in.Query, in.CaseSensitive))
	}
	err = p.walk(ctx, in.Folder, true, func(e entry) error {
		if e.info.IsDir() || path.Ext(e.name) != ".md" || e.info.Size() > int64(p.cfg.MaxNoteBytes) {
			return nil
		}
		n, err := p.load(e.name)
		if err != nil {
			return nil
		}
		scanned++
		if tag != "" && !hasTag(extractTags(n.text), tag) {
			return nil
		}
		name := e.name
		if !in.CaseSensitive {
			name = strings.ToLower(name)
		}
		if strings.Contains(name, query) {
			emit(e.name, 0, e.name)
			if hits >= max {
				return stopWalk
			}
		}
		for i, line := range noteLines(n.text) {
			if err := ctx.Err(); err != nil {
				return err
			}
			folded := line
			if !in.CaseSensitive {
				folded = strings.ToLower(line)
			}
			for cursor := 0; cursor < len(folded); {
				at := strings.Index(folded[cursor:], query)
				if at < 0 {
					break
				}
				center := utf8.RuneCountInString(folded[:cursor+at])
				hits++
				fmt.Fprintf(&out, "%s:%d: %s\n", e.name, i+1, snippetAt(line, center))
				if hits >= max {
					return stopWalk
				}
				cursor += at + len(query)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "total matches: %d; files scanned: %d; stopped at limit: %t\n", hits, scanned, hits >= max)
	return out.String(), nil
}

func snippet(text, query string, sensitive bool) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= 200 {
		return text
	}
	folded := text
	if !sensitive {
		folded = strings.ToLower(text)
		query = strings.ToLower(query)
	}
	at := strings.Index(folded, query)
	center := 0
	if at >= 0 {
		center = utf8.RuneCountInString(folded[:at])
	}
	return snippetAt(text, center)
}

func snippetAt(text string, center int) string {
	runes := []rune(text)
	if len(runes) <= 200 {
		return strings.TrimSpace(text)
	}
	start := max(0, center-80)
	start = min(start, len(runes)-200)
	return strings.TrimSpace(string(runes[start : start+200]))
}
