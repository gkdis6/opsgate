package note

import "github.com/gkdis6/opsgate/internal/redact"

func maskedText(text string) string {
	return redact.Fragment(text, redact.Ranges(text), 0, len(text))
}
