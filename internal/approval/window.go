package approval

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/gkdis6/opsgate/internal/audit"
	"github.com/gkdis6/opsgate/internal/redact"
	"github.com/gkdis6/opsgate/internal/tool"
)

const maxGrants = 100

type grantKey struct{ user, tool, target string }

func (m *Manager) windowEligible(name string, noWindow bool) bool {
	if noWindow || m.window < time.Minute || m.window > 12*time.Hour {
		return false
	}
	matched := false
	for _, pattern := range m.windowTools {
		ok, err := path.Match(pattern, name)
		if err != nil {
			return false
		}
		matched = matched || ok
	}
	return matched
}

// Caller holds m.mu and sweeps expired grants before creating one. Never evict
// another user's live grant to admit a new grant.
func (m *Manager) grantLocked(key grantKey, transport, method string, noWindow bool) (*audit.Event, error) {
	if !m.windowEligible(key.tool, noWindow) {
		return nil, tool.Denied("tool is not eligible for an approval window")
	}
	if _, exists := m.grants[key]; !exists && len(m.grants) >= maxGrants {
		return nil, tool.Denied("too many approval windows; try again after expiry")
	}
	now := m.now()
	expires := now.Add(m.window)
	m.grants[key] = expires
	args := map[string]any{"window": windowLabel(m.window), "expires_at": expires.UTC().Format(time.RFC3339Nano)}
	if key.target != "" {
		args["target"] = key.target
	}
	return &audit.Event{
		Time: now, User: redact.String(key.user), Transport: transport, Tool: key.tool,
		Outcome: "window_granted", Approval: method,
		Args: args,
	}, nil
}

func windowLabel(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return strings.TrimSuffix(d.String(), "0s")
	}
	return d.String()
}
