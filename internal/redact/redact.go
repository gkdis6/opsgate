// Package redact masks secrets in text before it leaves the server.
//
// Redaction is a safety net, not a guarantee: providers must still avoid
// reading secret material in the first place.
package redact

// Mask is the replacement for a redacted value.
const Mask = "[REDACTED]"

// String returns s with known secret patterns replaced by Mask.
func String(s string) string {
	// TODO(codex-C): implement.
	return s
}

// Value walks maps, slices and strings (as produced by encoding/json into
// any) and redacts string values, plus the whole value of keys that look
// sensitive (password, token, secret, ...).
func Value(v any) any {
	// TODO(codex-C): implement.
	return v
}
