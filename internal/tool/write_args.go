package tool

import (
	"bytes"
	"encoding/json"

	"github.com/gkdis6/opsgate/internal/redact"
)

func writeArgs(in any) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil || len(b) > 64<<10 {
		return nil, Denied("write arguments must be JSON of at most 64 KiB")
	}
	var args map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	// Approval binding must not round distinct integers through float64.
	d.UseNumber()
	if d.Decode(&args) != nil || args == nil {
		return nil, Denied("write arguments must be a JSON object")
	}
	delete(args, "approval_id")
	return redact.Value(args).(map[string]any), nil
}
