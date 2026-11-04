package note

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

func preview(d writePlan, before, after string) tool.Plan {
	var header string
	if d.action == "create" {
		header = fmt.Sprintf("create note %s\ncurrent sha256: absent\n", d.name)
	} else {
		currentHash := sha256.Sum256([]byte(redact.String(before)))
		header = fmt.Sprintf("%s note %s\ncurrent sha256: %x; size: %d; modified: %s\n", d.action, d.name, currentHash[:8], d.size, d.mtime.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
	if d.name != d.resolved {
		header += "resolved target: " + d.resolved + "\n"
	}
	resultHash := sha256.Sum256([]byte(redact.String(after)))
	header += fmt.Sprintf("result sha256: %x\n", resultHash)
	if d.action == "trash" {
		header += "destination: " + d.destination + " (retain in local trash)\n"
		header += "if changed during the move, check the error for the recovery location\n"
	}
	var body string
	if d.action == "create" {
		body = "new content:\n" + after
	} else {
		body = unifiedDiff(d.name, before, after)
	}
	body, truncated := truncatePreview(body, max(0, (8<<10)-len(header)-80))
	return tool.Plan{Summary: header + body, FullReviewRequired: truncated, Data: d}
}

func truncatePreview(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	// Prefer complete lines when possible, but bound even one enormous line.
	if newline := strings.LastIndexByte(text[:cut], '\n'); newline >= 0 {
		cut = newline + 1
	}
	more := len(strings.Split(strings.TrimSuffix(text[cut:], "\n"), "\n"))
	return text[:cut] + fmt.Sprintf("\n… %d more lines\n", more), true
}

func unifiedDiff(name, before, after string) string {
	split := func(text string) []string {
		if text == "" {
			return nil
		}
		lines := strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		return lines
	}
	old, new := split(before), split(after)
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix && old[len(old)-suffix-1] == new[len(new)-suffix-1] {
		suffix++
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", name, name)
	if before == after {
		return out.String()
	}
	start := max(0, prefix-3)
	context := min(3, suffix)
	oldEnd, newEnd := len(old)-suffix+context, len(new)-suffix+context
	oldStart, newStart := start+1, start+1
	if oldEnd == start {
		oldStart = start
	}
	if newEnd == start {
		newStart = start
	}
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldStart, oldEnd-start, newStart, newEnd-start)
	emit := func(prefix byte, line string) {
		out.WriteByte(prefix)
		out.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for _, line := range old[start:prefix] {
		emit(' ', line)
	}
	for _, line := range old[prefix : len(old)-suffix] {
		emit('-', line)
	}
	for _, line := range new[prefix : len(new)-suffix] {
		emit('+', line)
	}
	for _, line := range old[len(old)-suffix : oldEnd] {
		emit(' ', line)
	}
	return out.String()
}

func sectionEnd(text, wanted string) (int, error) {
	// Parse masked text, retaining raw offsets for the insertion.
	rawLines := strings.SplitAfter(text, "\n")
	ranges := redact.Ranges(text)
	lines := strings.SplitAfter(redact.Fragment(text, ranges, 0, len(text)), "\n")
	offset, matchAt, matchLevel, matches := 0, -1, 0, 0
	rangeAt := 0
	end := len(text)
	var fence byte
	width := 0
	front := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	for i, line := range lines {
		for rangeAt < len(ranges) && ranges[rangeAt].End <= offset {
			rangeAt++
		}
		hidden := rangeAt < len(ranges) && ranges[rangeAt].Start < offset+len(rawLines[i])
		trimmed := strings.TrimSpace(line)
		if front {
			if i > 0 && (trimmed == "---" || trimmed == "...") {
				front = false
			}
			offset += len(rawLines[i])
			continue
		}
		left := strings.TrimLeft(line, " ")
		indent := len(line) - len(left)
		if indent <= 3 && len(left) >= 3 && (left[0] == '`' || left[0] == '~') {
			n := 0
			for n < len(left) && left[n] == left[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence = left[0]
					width = n
				} else if fence == left[0] && n >= width && strings.TrimSpace(left[n:]) == "" {
					fence = 0
				}
				offset += len(rawLines[i])
				continue
			}
		}
		if fence == 0 && indent <= 3 {
			level := 0
			for level < len(left) && left[level] == '#' {
				level++
			}
			if level >= 1 && level <= 6 && level < len(left) && (left[level] == ' ' || left[level] == '\t') {
				heading := strings.TrimSpace(left[level:])
				closing := strings.TrimRight(heading, "#")
				if closing != heading && len(closing) > 0 && (closing[len(closing)-1] == ' ' || closing[len(closing)-1] == '\t') {
					heading = strings.TrimSpace(closing)
				}
				if matchAt >= 0 && end == len(text) && level <= matchLevel {
					end = offset
				}
				if heading == wanted && !hidden {
					matches++
					if matches == 1 {
						matchAt = offset
						matchLevel = level
					}
				}
			}
		}
		offset += len(rawLines[i])
	}
	if matches == 0 {
		return 0, fmt.Errorf("heading absent")
	}
	if matches > 1 {
		return 0, fmt.Errorf("heading ambiguous")
	}
	return end, nil
}
