package alert

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// ReadSecret loads the same trimmed key on both ends, with bounded file reads.
func ReadSecret(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read secret_file")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) > 8192 {
		return nil, fmt.Errorf("secret_file exceeds 8192 bytes or cannot be read")
	}
	b = bytes.TrimSpace(b)
	if len(b) < 16 || len(b) > 4096 {
		return nil, fmt.Errorf("secret_file must hold 16 to 4096 bytes")
	}
	return b, nil
}
