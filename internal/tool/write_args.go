package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"

	"github.com/gkdis6/opsgate/internal/redact"
)

func writeArgs(in any, maxBytes int) (map[string]any, [sha256.Size]byte, error) {
	b, ok := in.(json.RawMessage)
	if !ok {
		var err error
		b, err = json.Marshal(in)
		if err != nil {
			return nil, [sha256.Size]byte{}, Denied("write arguments must be JSON")
		}
	}
	if len(b) > maxBytes {
		return nil, [sha256.Size]byte{}, Denied("write arguments must be JSON of at most %d KiB", maxBytes>>10)
	}
	if !json.Valid(b) {
		return nil, [sha256.Size]byte{}, Denied("write arguments must be JSON")
	}
	var args map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	// Approval binding must not round distinct integers through float64.
	d.UseNumber()
	if d.Decode(&args) != nil || args == nil {
		return nil, [sha256.Size]byte{}, Denied("write arguments must be a JSON object")
	}
	delete(args, "approval_id")
	canonical, err := json.Marshal(args)
	if err != nil {
		return nil, [sha256.Size]byte{}, Denied("write arguments must be JSON")
	}
	redacted := redact.Value(args).(map[string]any)
	display, err := json.Marshal(redacted)
	if err != nil || len(display) > maxBytes {
		return nil, [sha256.Size]byte{}, Denied("redacted write arguments exceed %d KiB limit", maxBytes>>10)
	}
	return redacted, sha256.Sum256(canonical), nil
}
