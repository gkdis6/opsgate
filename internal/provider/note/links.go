package note

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
)

var wikiLink = regexp.MustCompile(`!?\[\[([^\]\n]+)\]\]`)
var markdownLink = regexp.MustCompile(`!?\[[^\]\n]*\]\(([^)\n]+)\)`)

type link struct {
	target   string
	relative bool
}

func extractLinks(text string) []link {
	var links []link
	for _, line := range prose(text) {
		for _, match := range wikiLink.FindAllStringSubmatch(line, -1) {
			target := strings.TrimSpace(strings.SplitN(strings.SplitN(match[1], "|", 2)[0], "#", 2)[0])
			links = append(links, link{target: target})
		}
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			raw := strings.TrimSpace(match[1])
			if strings.HasPrefix(raw, "<") {
				if end := strings.Index(raw, ">"); end >= 0 {
					raw = raw[1:end]
				}
			} else {
				fields := strings.Fields(raw)
				if len(fields) == 0 {
					continue
				}
				raw = fields[0]
			}
			u, err := url.Parse(raw)
			if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" {
				continue
			}
			target, err := url.PathUnescape(u.EscapedPath())
			if err != nil || !strings.HasSuffix(target, ".md") {
				continue
			}
			links = append(links, link{target: target, relative: true})
		}
	}
	return links
}

func (p *provider) resolveLink(source string, l link, notes map[string]bool, basenames map[string][]string) string {
	target := l.target
	if target == "" {
		return source
	}
	if strings.HasPrefix(target, "/") || strings.Contains(target, "\\") {
		return ""
	}
	if !strings.HasSuffix(target, ".md") {
		target += ".md"
	}
	candidates := []string{target, path.Join(path.Dir(source), target)}
	if l.relative {
		candidates = []string{path.Join(path.Dir(source), target)}
	}
	for _, candidate := range candidates {
		if p.validPath(candidate, false) == nil && notes[candidate] {
			return candidate
		}
	}
	if !l.relative && !strings.Contains(target, "/") {
		if matches := basenames[target]; len(matches) == 1 {
			return matches[0]
		}
	}
	return ""
}

func (p *provider) links(ctx context.Context, _ tool.Caller, in pathInput) (string, error) {
	name, err := p.notePath(in.Path)
	if err != nil {
		return "", err
	}
	n, err := p.load(name)
	if err != nil {
		return "", err
	}
	name = n.resolved
	notes := map[string]bool{}
	basenames := map[string][]string{}
	err = p.walk(ctx, "", true, func(e entry) error {
		if !e.info.IsDir() && path.Ext(e.name) == ".md" {
			notes[e.name] = true
			base := path.Base(e.name)
			basenames[base] = append(basenames[base], e.name)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintln(&out, "outgoing:")
	emitted := 0
	for _, l := range extractLinks(maskedText(n.text)) {
		if emitted >= 1000 {
			fmt.Fprintln(&out, "truncated: outgoing link limit 1000")
			break
		}
		emitted++
		target := p.resolveLink(name, l, notes, basenames)
		if target == "" {
			fmt.Fprintf(&out, "%s -> unresolved\n", l.target)
		} else {
			fmt.Fprintf(&out, "%s -> %s\n", l.target, target)
		}
	}
	fmt.Fprintln(&out, "backlinks:")
	names := make([]string, 0, len(notes))
	for other := range notes {
		names = append(names, other)
	}
	sort.Strings(names)
	backlinks := 0
	for _, other := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if other == name {
			continue
		}
		n, err := p.load(other)
		if err != nil {
			continue
		}
		for _, l := range extractLinks(maskedText(n.text)) {
			if p.resolveLink(other, l, notes, basenames) == name {
				fmt.Fprintln(&out, other)
				backlinks++
				break
			}
		}
		if backlinks >= 1000 {
			fmt.Fprintln(&out, "truncated: backlink limit 1000")
			break
		}
	}
	return out.String(), nil
}
