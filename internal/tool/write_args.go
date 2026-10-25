package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"

	"github.com/gkdis6/opsgate/internal/redact"
)

func writeArgs(in any) (map[string]any, [sha256.Size]byte, error) {
	b, err := json.Marshal(in)
	if err != nil || len(b) > 64<<10 {
		return nil, [sha256.Size]byte{}, Denied("write arguments must be JSON of at most 64 KiB")
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
	return redact.Value(args).(map[string]any), sha256.Sum256(canonical), nil
}
