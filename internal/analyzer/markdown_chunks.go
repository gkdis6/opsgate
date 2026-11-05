package analyzer

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
)

var markdownAtom = regexp.MustCompile("!?\\[[^\\]\\n]*\\]\\([^\\s\\n]*\\)|https?://[^\\s<>]+|`[^`\\n]+`")

// chunks packs paragraphs and fenced code blocks without splitting links or
// ordinary code blocks. An oversized code block is continued in balanced fences.
func chunks(text string, limit int) []string {
	var blocks []string
	var block strings.Builder
	fence := ""
	flush := func() {
		if block.Len() > 0 {
			blocks = append(blocks, block.String())
			block.Reset()
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		marker := ""
		if strings.HasPrefix(trimmed, "```") {
			marker = "```"
		} else if strings.HasPrefix(trimmed, "~~~") {
			marker = "~~~"
		}
		if fence == "" && marker != "" {
			flush()
			fence = marker
			block.WriteString(line)
			continue
		}
		block.WriteString(line)
		if fence != "" {
			if marker == fence {
				fence = ""
				flush()
			}
			continue
		}
		if trimmed == "" {
			flush()
		}
	}
	flush()
	var parts []string
	current := ""
	emit := func(part string) {
		if current != "" && units(current)+units(part) > limit {
			parts = append(parts, current)
			current = ""
		}
		current += part
	}
	for _, b := range blocks {
		if units(b) <= limit {
			emit(b)
			continue
		}
		// Keep a heading with its following paragraph where the combined block fits.
		pieces := []string{}
		first, rest, code := strings.Cut(b, "\n")
		if code && (strings.HasPrefix(strings.TrimSpace(first), "```") || strings.HasPrefix(strings.TrimSpace(first), "~~~")) {
			marker := strings.TrimSpace(first)[:3]
			end := strings.LastIndex(rest, marker)
			if end >= 0 {
				rest = rest[:end]
			}
			overhead := units(first + "\n\n" + marker + "\n")
			for _, body := range proseChunks(rest, limit-overhead) {
				pieces = append(pieces, first+"\n"+strings.TrimSuffix(body, "\n")+"\n"+marker+"\n")
			}
		} else {
			pieces = proseChunks(b, limit)
		}
		for _, piece := range pieces {
			emit(piece)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func units(s string) int { return len(utf16.Encode([]rune(s))) }

func proseChunks(text string, limit int) []string {
	var result []string
	for units(text) > limit {
		maxEnd, count := 0, 0
		for i, r := range text {
			n := 1
			if r > 0xffff {
				n = 2
			}
			if count+n > limit {
				break
			}
			count += n
			maxEnd = i + len(string(r))
		}
		if maxEnd == 0 {
			break
		}
		protected := markdownAtom.FindAllStringIndex(text, -1)
		safe := func(pos int) bool {
			for _, span := range protected {
				if pos > span[0] && pos < span[1] {
					return false
				}
			}
			return true
		}
		newline, space := 0, 0
		for i, r := range text[:maxEnd] {
			pos := i + len(string(r))
			if !safe(pos) {
				continue
			}
			if r == '\n' {
				newline = pos
			}
			if unicode.IsSpace(r) {
				space = pos
			}
		}
		end := newline
		if end == 0 {
			end = space
		}
		if end == 0 {
			end = maxEnd
			// An atom that fits a fresh message moves there intact.
			for _, span := range protected {
				if end > span[0] && end < span[1] && span[0] > 0 {
					end = span[0]
					break
				}
			}
		}
		result = append(result, text[:end])
		text = text[end:]
	}
	if text != "" {
		result = append(result, text)
	}
	return result
}
