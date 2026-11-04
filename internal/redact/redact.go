// Package redact masks secrets in text before it leaves the server.
//
// Redaction is a safety net, not a guarantee: providers must still avoid
// reading secret material in the first place.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

// Mask is the replacement for a redacted value.
const Mask = "[REDACTED]"

var (
	pemMarker       = regexp.MustCompile(`-----(BEGIN|END) [A-Z0-9 ]*PRIVATE KEY-----`)
	header          = regexp.MustCompile(`(?im)(\b(?:proxy-)?authorization[ \t]*:[ \t]*)[^\r\n]+`)
	auth            = regexp.MustCompile(`(?i)\b(Bearer|Basic)[ \t]+[A-Za-z0-9._~+/=-]+`)
	jwt             = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	tokens          = regexp.MustCompile(`\b(?:AKIA[A-Z0-9]{16}|ASIA[A-Z0-9]{16}|gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|glpat-[A-Za-z0-9_-]{8,}|xox[abprs]-[A-Za-z0-9-]{8,}|[sr]k_live_[A-Za-z0-9]{8,}|sk-(?:ant-)?[A-Za-z0-9_-]{20,}|AIza[A-Za-z0-9_-]{35}|[0-9]{8,10}:[A-Za-z0-9_-]{35}|og_[ars]t_[A-Za-z0-9_-]{43,})`)
	urlPassword     = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]+:)[^\s/@]*(@)`)
	assignmentValue = regexp.MustCompile(`^("(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;"'<>{}]+)`)
)

// Range is a sensitive byte interval [Start, End) in the original text.
type Range struct {
	Start int
	End   int
}

// Ranges returns sorted, non-overlapping sensitive ranges in s.
// Incomplete private-key PEM blocks extend to the source boundary.
// Quoted assignments left open on their line extend to the line boundary.
func Ranges(s string) []Range {
	var ranges []Range
	if strings.Contains(s, "PRIVATE KEY-----") {
		start := -1
		for _, m := range pemMarker.FindAllStringSubmatchIndex(s, -1) {
			if s[m[2]:m[3]] == "BEGIN" {
				if start < 0 {
					start = m[0]
				}
			} else {
				if start < 0 {
					start = 0
				}
				ranges = append(ranges, Range{start, m[1]})
				start = -1
			}
		}
		if start >= 0 {
			ranges = append(ranges, Range{start, len(s)})
		}
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "authorization") {
		for _, m := range header.FindAllStringSubmatchIndex(s, -1) {
			ranges = append(ranges, Range{m[3], m[1]})
		}
	}
	ranges = append(ranges, assignmentRanges(s)...)
	if strings.Contains(lower, "bearer") || strings.Contains(lower, "basic") {
		for _, m := range auth.FindAllStringSubmatchIndex(s, -1) {
			start := m[3]
			for s[start] == ' ' || s[start] == '\t' {
				start++
			}
			ranges = append(ranges, Range{start, m[1]})
		}
	}
	if strings.Contains(s, "eyJ") {
		for _, m := range jwt.FindAllStringIndex(s, -1) {
			ranges = append(ranges, Range{m[0], m[1]})
		}
	}
	if hasToken(s) {
		for _, m := range tokens.FindAllStringIndex(s, -1) {
			ranges = append(ranges, Range{m[0], m[1]})
		}
	}
	if strings.Contains(s, "://") {
		for _, m := range urlPassword.FindAllStringSubmatchIndex(s, -1) {
			ranges = append(ranges, Range{m[3], m[4]})
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	merged := ranges[:0]
	for _, r := range ranges {
		if len(merged) > 0 && r.Start < merged[len(merged)-1].End {
			merged[len(merged)-1].End = max(merged[len(merged)-1].End, r.End)
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

// String returns s with known secret patterns replaced by Mask.
func String(s string) string {
	return render(s, Ranges(s), 0, len(s), false)
}

// Fragment renders s[start:end] using ranges computed over the full s.
// Each visible part of an intersecting secret is masked, even at page boundaries.
// CR and LF bytes are preserved, masking each nonempty part of a secret line
// separately so line numbers and line endings remain stable.
// Bounds must satisfy 0 <= start <= end <= len(s); ranges must come from Ranges(s).
func Fragment(s string, ranges []Range, start, end int) string {
	return render(s, ranges, start, end, true)
}

func render(s string, ranges []Range, start, end int, preserveLines bool) string {
	var out strings.Builder
	cursor := start
	first := sort.Search(len(ranges), func(i int) bool { return ranges[i].End >= start })
	for _, r := range ranges[first:] {
		if r.Start >= end {
			break
		}
		lo, hi := max(start, r.Start), min(end, r.End)
		if lo > hi || lo == hi && r.Start != r.End {
			continue
		}
		out.WriteString(s[cursor:lo])
		if !preserveLines {
			out.WriteString(Mask)
		} else {
			for lo < hi {
				next := strings.IndexAny(s[lo:hi], "\r\n")
				if next < 0 {
					out.WriteString(Mask)
					break
				}
				if next > 0 {
					out.WriteString(Mask)
				}
				out.WriteByte(s[lo+next])
				lo += next + 1
			}
			if r.Start == r.End {
				out.WriteString(Mask)
			}
		}
		cursor = hi
	}
	if cursor == start && out.Len() == 0 {
		return s[start:end]
	}
	out.WriteString(s[cursor:end])
	return out.String()
}

// Scan separators directly rather than running a large regexp over every log
// word. Whitespace is a separator only for long flags and uppercase env keys.
func assignmentRanges(s string) []Range {
	var ranges []Range
	pos, authorityEnd := 0, 0
	hasFlags := strings.Contains(s, "--")
	separators := ":="
	if hasFlags {
		separators = ":= \t"
	} else {
		// All sensitive env keys contain one of these uppercase endings.
		// Keep the usual separator scan for text without any candidates.
		for _, ending := range []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "CREDENTIAL", "PWD", "ACCESS_KEY", "SECRET_KEY", "PRIVATE_KEY", "SESSION_ID", "COOKIE", "INVITE"} {
			if strings.Contains(s, ending) {
				separators = ":= \t"
				break
			}
		}
	}
	for pos < len(s) {
		next := strings.IndexAny(s[pos:], separators)
		if next < 0 {
			break
		}
		sep := pos + next
		pos = sep + 1
		space := s[sep] == ' ' || s[sep] == '\t'
		// Visit a whitespace run once. Looking back from every byte in a
		// run would repeatedly scan the same prefix (quadratic on log input).
		if space {
			for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
				pos++
			}
		}
		// Sensitive env keys end in an uppercase letter. Ordinary log words
		// need no backward scan unless the text also contains long flags.
		if space && !hasFlags && (sep == 0 || s[sep-1] < 'A' || s[sep-1] > 'Z') {
			continue
		}
		if s[sep] == ':' {
			if strings.HasPrefix(s[pos:], "//") {
				authorityEnd = len(s)
				if end := strings.IndexAny(s[pos+2:], "/?# \t\r\n"); end >= 0 {
					authorityEnd = pos + 2 + end
				}
				continue
			}
			if sep < authorityEnd {
				continue
			}
		}
		end := sep
		for end > 0 && (s[end-1] == ' ' || s[end-1] == '\t') {
			end--
		}
		if !space && end > 0 && (s[end-1] == '"' || s[end-1] == '\'') {
			end--
		}
		start := end
		for start > 0 && keyByte(s[start-1]) {
			start--
		}
		if start == end {
			continue
		}
		key := s[start:end]
		if space && !envKey(key) && !(strings.HasPrefix(key, "--") &&
			(start == 0 || s[start-1] == ' ' || s[start-1] == '\t' || s[start-1] == '\n')) {
			continue
		}
		if !SensitiveKey(strings.TrimPrefix(key, "--")) {
			continue
		}
		for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
			pos++
		}
		if space && pos < len(s) && (s[pos] == '=' || s[pos] == ':') {
			continue
		}
		value := assignmentValue.FindString(s[pos:])
		complete := value != ""
		if !complete {
			value = quotedTail(s[pos:])
		}
		if value == "" {
			continue
		}
		// Keep the rest of an invitation URL visible.
		if strings.EqualFold(key, "invite") && start > 0 && (s[start-1] == '?' || s[start-1] == '&') && value[0] != '"' && value[0] != '\'' {
			if end := strings.IndexAny(value, "&#"); end >= 0 {
				value = value[:end]
			}
		}
		valueStart, valueEnd := pos, pos+len(value)
		if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
			valueStart++
			if complete {
				valueEnd--
			}
		}
		ranges = append(ranges, Range{valueStart, valueEnd})
		pos += len(value)
	}
	return ranges
}

// quotedTail returns a quoted value left open on its line, through that line.
// A later quote may belong to other text, so it never closes this value.
func quotedTail(s string) string {
	if len(s) == 0 || s[0] != '"' && s[0] != '\'' {
		return ""
	}
	if end := strings.IndexAny(s, "\r\n"); end >= 0 {
		return s[:end]
	}
	return s
}

// SensitiveKey reports whether a key's value is masked by Value.
func SensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, suffix := range []string{"password", "passwd", "secret", "token", "apikey", "api_key", "credential", "credentials"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	for {
		switch k {
		case "password", "passwd", "pwd", "secret", "client_secret", "token", "access_token", "refresh_token", "api_key", "api-key", "apikey", "access_key", "secret_key", "private_key", "credential", "credentials", "auth_token", "session_id", "cookie", "set-cookie", "invite":
			return true
		}
		i := strings.IndexByte(k, '_')
		if i <= 0 {
			return false
		}
		for j := 0; j < i; j++ {
			if c := k[j]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return false
			}
		}
		k = k[i+1:]
	}
}

// SensitiveFlag reports whether a standalone long flag takes a secret value.
// Short flags are deliberately excluded because their meanings vary by program.
func SensitiveFlag(flag string) bool {
	return strings.HasPrefix(flag, "--") && strings.IndexAny(flag, "=: \t\r\n") < 0 && SensitiveKey(strings.TrimPrefix(flag, "--"))
}

func envKey(k string) bool {
	if k[0] != '_' && (k[0] < 'A' || k[0] > 'Z') {
		return false
	}
	for i := 1; i < len(k); i++ {
		c := k[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func keyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func hasToken(s string) bool {
	for _, prefix := range []string{"AKIA", "ASIA", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "sk-", "sk_live_", "rk_live_", "AIza", "og_at_", "og_rt_", "og_st_"} {
		if strings.Contains(s, prefix) {
			return true
		}
	}
	// Telegram tokens have a long numeric id immediately before a colon.
	for pos := 0; pos < len(s); {
		next := strings.IndexByte(s[pos:], ':')
		if next < 0 {
			break
		}
		colon := pos + next
		start := colon
		for start > 0 && colon-start <= 10 && s[start-1] >= '0' && s[start-1] <= '9' {
			start--
		}
		if n := colon - start; n >= 8 && n <= 10 {
			return true
		}
		pos = colon + 1
	}
	return false
}

// Value walks maps, slices and strings (as produced by encoding/json into
// any) and redacts string values, plus the whole value of sensitive keys.
// Containers are copied so the caller's data is not modified.
func Value(v any) any {
	switch v := v.(type) {
	case string:
		return String(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, value := range v {
			if SensitiveKey(k) {
				out[k] = Mask
			} else {
				out[k] = Value(value)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = Value(value)
		}
		return out
	default:
		return v
	}
}
