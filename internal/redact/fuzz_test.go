package redact

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Secrets that each pattern must still mask when surrounded by arbitrary
// text on other lines.
var fuzzSecrets = []struct{ line, secret string }{
	{"password=fuzzsecret1", "fuzzsecret1"},
	{`DB_PASSWORD="fuzz secret 2"`, "fuzz secret 2"},
	{"Authorization: Bearer fuzzsecret3abc", "fuzzsecret3abc"},
	{"token: ghp_fuzzsecret4fuzzsecret4", "ghp_fuzzsecret4fuzzsecret4"},
	{"url=https://user:fuzzsecret5@db.example/x", "fuzzsecret5"},
	{"--api-key fuzzsecret6", "fuzzsecret6"},
}

func FuzzRanges(f *testing.F) {
	for _, seed := range []string{
		"", "plain text", "password=\"open\nnext\" x", "-----BEGIN RSA PRIVATE KEY-----\nabc",
		"a=b c:d --token x", "eyJa.eyJb.c", "postgres://u:p@h/db", "TOKEN 'x\\'y'", "\x00\xff\"'\\",
		strings.Repeat("password=\"x\n", 20),
	} {
		f.Add(seed, uint16(0), uint16(len(seed)))
	}
	f.Fuzz(func(t *testing.T, s string, a, b uint16) {
		ranges := Ranges(s)
		prev := -1
		for _, r := range ranges {
			if r.Start < 0 || r.End > len(s) || r.Start > r.End || r.Start < prev {
				t.Fatalf("bad ranges %v for %q", ranges, s)
			}
			prev = r.End
		}
		out := String(s)
		if len(ranges) == 0 && out != s {
			t.Fatalf("changed text without ranges: %q -> %q", s, out)
		}

		start, end := int(a)%(len(s)+1), int(b)%(len(s)+1)
		if start > end {
			start, end = end, start
		}
		page := Fragment(s, ranges, start, end)
		// Line numbers and endings of every page must stay stable.
		for _, c := range []string{"\n", "\r"} {
			if strings.Count(page, c) != strings.Count(s[start:end], c) {
				t.Fatalf("page %d:%d changed %q count: %q -> %q", start, end, c, s[start:end], page)
			}
		}
		for _, r := range ranges {
			lo, hi := max(start, r.Start), min(end, r.End)
			for _, part := range strings.FieldsFunc(s[lo:max(lo, hi)], func(r rune) bool { return r == '\n' || r == '\r' }) {
				if len(part) >= 8 && strings.Contains(page, part) && !strings.Contains(stripRanges(s[start:end], start, ranges), part) {
					t.Fatalf("secret part %q visible in page %q", part, page)
				}
			}
		}

		// A known secret on its own line stays masked whatever surrounds it.
		if utf8.ValidString(s) {
			for _, known := range fuzzSecrets {
				in := s + "\n" + known.line + "\n" + s
				if strings.Contains(String(in), known.secret) {
					t.Fatalf("%q visible in %q", known.secret, String(in))
				}
			}
		}
	})
}

// stripRanges returns the visible text of s (which starts at offset base),
// so a fragment repeating visible text is not mistaken for a leak.
func stripRanges(s string, base int, ranges []Range) string {
	var out strings.Builder
	cursor := base
	for _, r := range ranges {
		lo, hi := max(r.Start, base), min(r.End, base+len(s))
		if lo >= hi {
			continue
		}
		if lo > cursor {
			out.WriteString(s[cursor-base : lo-base])
		}
		out.WriteByte(0)
		cursor = max(cursor, hi)
	}
	if cursor < base+len(s) {
		out.WriteString(s[cursor-base:])
	}
	return out.String()
}

func FuzzValue(f *testing.F) {
	f.Add("password", "hunter2", "note")
	f.Fuzz(func(t *testing.T, key, value, other string) {
		if key == "list" {
			return
		}
		in := map[string]any{key: value, "list": []any{other, map[string]any{key: value}}}
		out := Value(in).(map[string]any)
		if SensitiveKey(key) && (out[key] != Mask || out["list"].([]any)[1].(map[string]any)[key] != Mask) {
			t.Fatalf("sensitive key %q not masked: %v", key, out)
		}
		if in[key] != value {
			t.Fatal("input modified")
		}
	})
}
