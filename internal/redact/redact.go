// Package redact masks secrets in text before it leaves the server.
//
// Redaction is a safety net, not a guarantee: providers must still avoid
// reading secret material in the first place.
package redact

import (
	"regexp"
	"strings"
)

// Mask is the replacement for a redacted value.
const Mask = "[REDACTED]"

var (
	pem             = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	header          = regexp.MustCompile(`(?im)(\b(?:proxy-)?authorization[ \t]*:[ \t]*)[^\r\n]+`)
	auth            = regexp.MustCompile(`(?i)\b(Bearer|Basic)[ \t]+[A-Za-z0-9._~+/=-]+`)
	jwt             = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	tokens          = regexp.MustCompile(`\b(?:AKIA[A-Z0-9]{16}|ASIA[A-Z0-9]{16}|gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|glpat-[A-Za-z0-9_-]{8,}|xox[abprs]-[A-Za-z0-9-]{8,}|[sr]k_live_[A-Za-z0-9]{8,}|sk-(?:ant-)?[A-Za-z0-9_-]{20,}|AIza[A-Za-z0-9_-]{35}|[0-9]{8,10}:[A-Za-z0-9_-]{35})`)
	urlPassword     = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]+:)[^\s/@]*(@)`)
	assignmentValue = regexp.MustCompile(`^("(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|[^\s,;"'<>{}]+)`)
)

// String returns s with known secret patterns replaced by Mask.
func String(s string) string {
	if strings.Contains(s, "-----BEGIN ") {
		s = pem.ReplaceAllString(s, Mask)
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "authorization") {
		s = header.ReplaceAllString(s, "${1}"+Mask)
	}
	s = redactAssignments(s)
	if strings.Contains(lower, "bearer") || strings.Contains(lower, "basic") {
		s = auth.ReplaceAllString(s, "${1} "+Mask)
	}
	if strings.Contains(s, "eyJ") {
		s = jwt.ReplaceAllString(s, Mask)
	}
	if hasToken(s) {
		s = tokens.ReplaceAllString(s, Mask)
	}
	if strings.Contains(s, "://") {
		s = urlPassword.ReplaceAllString(s, "${1}"+Mask+"${2}")
	}
	return s
}

// Most operation text contains separators but no sensitive keys. Scan those
// separators directly rather than running a large regexp over every log word.
func redactAssignments(s string) string {
	var out strings.Builder
	cursor, pos, authorityEnd := 0, 0, 0
	for pos < len(s) {
		next := strings.IndexAny(s[pos:], ":=")
		if next < 0 {
			break
		}
		sep := pos + next
		pos = sep + 1
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
		if end > 0 && (s[end-1] == '"' || s[end-1] == '\'') {
			end--
		}
		start := end
		for start > 0 && keyByte(s[start-1]) {
			start--
		}
		if start == end || !sensitiveKey(s[start:end]) {
			continue
		}
		for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
			pos++
		}
		value := assignmentValue.FindString(s[pos:])
		if value == "" {
			continue
		}
		out.WriteString(s[cursor:pos])
		if value[0] == '"' || value[0] == '\'' {
			out.WriteByte(value[0])
			out.WriteString(Mask)
			out.WriteByte(value[0])
		} else {
			out.WriteString(Mask)
		}
		cursor = pos + len(value)
		pos = cursor
	}
	if cursor == 0 {
		return s
	}
	out.WriteString(s[cursor:])
	return out.String()
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for {
		switch k {
		case "password", "passwd", "pwd", "secret", "client_secret", "token", "access_token", "refresh_token", "api_key", "apikey", "access_key", "secret_key", "private_key", "credential", "credentials", "auth_token", "session_id", "cookie", "set-cookie":
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

func keyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func hasToken(s string) bool {
	for _, prefix := range []string{"AKIA", "ASIA", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "sk-", "sk_live_", "rk_live_", "AIza"} {
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
			if sensitiveKey(k) {
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
