package host

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/gkdis6/opsgate/internal/tool"
)

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]+$`)
var unitSuffixes = []string{".service", ".timer", ".socket", ".mount", ".target", ".path", ".slice", ".scope"}

func hasSuffix(s string) bool {
	for _, suffix := range unitSuffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}
func normalizeUnit(s string) (string, error) {
	if s == "" || len(s) > 256 || strings.HasPrefix(s, "-") || !unitName.MatchString(s) {
		return "", fmt.Errorf("invalid unit name")
	}
	if !hasSuffix(s) {
		s += ".service"
	}
	if len(s) > 256 {
		return "", fmt.Errorf("unit name exceeds 256 bytes")
	}
	return s, nil
}
func normalizePattern(s string) (string, error) {
	if s == "" || len(s) > 256 || strings.HasPrefix(s, "-") || strings.ContainsAny(s, "/\n\r\x00") {
		return "", fmt.Errorf("invalid unit allowlist pattern %q", s)
	}
	if !hasSuffix(s) && !strings.HasSuffix(s, "*") && !strings.HasSuffix(s, "?") && !strings.HasSuffix(s, "]") {
		s += ".service"
	}
	if _, err := path.Match(s, ""); err != nil {
		return "", fmt.Errorf("invalid unit allowlist pattern %q: %w", s, err)
	}
	return s, nil
}
func (p *provider) allowed(raw string) (string, error) {
	unit, err := normalizeUnit(raw)
	if err != nil {
		return "", err
	}
	for _, pattern := range p.cfg.Units {
		pattern, err = normalizePattern(pattern)
		if err != nil {
			continue
		}
		if ok, _ := path.Match(pattern, unit); ok {
			return unit, nil
		}
	}
	return "", tool.Denied("unit is not allowed")
}
