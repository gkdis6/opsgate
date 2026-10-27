package note

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

func cleanTag(tag string) (string, error) {
	tag = strings.TrimPrefix(tag, "#")
	if len(tag) > 256 || !utf8.ValidString(tag) {
		return "", fmt.Errorf("invalid tag")
	}
	for _, r := range tag {
		if !tagRune(r) {
			return "", fmt.Errorf("invalid tag")
		}
	}
	if tag != "" && (strings.HasPrefix(tag, "/") || strings.HasSuffix(tag, "/") || strings.Contains(tag, "//")) {
		return "", fmt.Errorf("invalid tag")
	}
	return tag, nil
}
func tagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_' || r == '-' || r == '/'
}
func hasTag(tags map[string]bool, tag string) bool {
	for t := range tags {
		if t == tag || strings.HasPrefix(t, tag+"/") {
			return true
		}
	}
	return false
}

func frontmatter(text string) (string, string) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSuffix(lines[0], "\r") != "---" {
		return "", text
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSuffix(lines[i], "\r") == "---" || strings.TrimSuffix(lines[i], "\r") == "..." {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return "", text
}

func prose(text string) []string {
	_, body := frontmatter(text)
	var out []string
	var fence byte
	width := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if indent <= 3 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := 0
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence = trimmed[0]
					width = n
					continue
				}
				if fence == trimmed[0] && n >= width && strings.TrimSpace(trimmed[n:]) == "" {
					fence = 0
					continue
				}
			}
		}
		if fence == 0 && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") {
			out = append(out, line)
		}
	}
	return out
}

func extractTags(text string) map[string]bool {
	tags := map[string]bool{}
	add := func(value string) {
		for _, v := range strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == ',' }) {
			if t, err := cleanTag(v); err == nil && t != "" {
				tags[t] = true
			}
		}
	}
	fm, _ := frontmatter(text)
	var doc yaml.Node
	if fm != "" && yaml.Unmarshal([]byte(fm), &doc) == nil && len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		fields := doc.Content[0].Content
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i].Value == "tags" || fields[i].Value == "tag" {
				v := fields[i+1]
				if v.Kind == yaml.ScalarNode {
					add(v.Value)
				}
				if v.Kind == yaml.SequenceNode {
					for _, child := range v.Content {
						if child.Kind == yaml.ScalarNode {
							add(child.Value)
						}
					}
				}
			}
		}
	}
	for _, line := range prose(text) {
		trimmed := strings.TrimSpace(line)
		heading := strings.TrimLeft(trimmed, "#")
		if len(heading) < len(trimmed) && strings.HasPrefix(heading, " ") {
			continue
		}
		runes := []rune(line)
		inCode := false
		for i := 0; i < len(runes); i++ {
			if runes[i] == '`' {
				inCode = !inCode
				continue
			}
			if inCode || runes[i] != '#' || (i > 0 && (tagRune(runes[i-1]) || runes[i-1] == '\\')) {
				continue
			}
			end := i + 1
			for end < len(runes) && tagRune(runes[end]) {
				end++
			}
			// Like Obsidian, a purely numeric "#1036" is not a tag.
			if end > i+1 && strings.ContainsFunc(string(runes[i+1:end]), func(r rune) bool { return r < '0' || r > '9' }) {
				add(string(runes[i+1 : end]))
			}
			if end > i+1 {
				i = end - 1
			}
		}
	}
	return tags
}
