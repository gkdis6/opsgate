// Package hosttarget defines the bounded identifiers accepted by host tools
// and navigation links. Names are always concrete, never shell expressions.
package hosttarget

import (
	"path"
	"regexp"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
)

var unit = regexp.MustCompile(`^[A-Za-z0-9:_.@-]+\.(service|timer|socket|mount|target|path|slice|scope|automount|swap)$`)
var container = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func Valid(kind, name string) bool {
	if len(name) == 0 || len(name) > 256 || strings.HasPrefix(name, "-") || redact.String(name) != name || strings.Contains(name, redact.Mask) {
		return false
	}
	switch kind {
	case "unit":
		return unit.MatchString(name)
	case "container":
		return container.MatchString(name)
	}
	return false
}

func Match(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
