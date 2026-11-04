package note

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
)

type tagInput struct {
	Tag string `json:"tag,omitempty" jsonschema:"Optional exact tag, with or without #; includes nested tags. Empty returns counts."`
}

func (p *provider) tags(ctx context.Context, _ tool.Caller, in tagInput) (string, error) {
	tag, err := cleanTag(in.Tag)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	var notes []string
	err = p.walk(ctx, "", true, func(e entry) error {
		if e.info.IsDir() || path.Ext(e.name) != ".md" || e.info.Size() > int64(p.cfg.MaxNoteBytes) {
			return nil
		}
		n, err := p.load(e.name)
		if err != nil {
			return nil
		}
		tags := extractTags(maskedText(n.text))
		if tag != "" {
			if hasTag(tags, tag) {
				notes = append(notes, e.name)
			}
		} else {
			for t := range tags {
				counts[t]++
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(notes)
	var out strings.Builder
	if tag != "" {
		for _, name := range notes {
			fmt.Fprintln(&out, name)
		}
	} else {
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&out, "#%s\t%d notes\n", name, counts[name])
		}
	}
	return out.String(), nil
}
