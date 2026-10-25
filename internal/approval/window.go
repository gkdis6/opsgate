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

type grantKey struct{ user, tool string }

func (m *Manager) windowEligible(name string) bool {
	if m.window < time.Minute || m.window > 12*time.Hour {
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
func (m *Manager) grantLocked(user, transport, name, method string) (*audit.Event, error) {
	if !m.windowEligible(name) {
		return nil, tool.Denied("tool is not eligible for an approval window")
	}
	key := grantKey{user, name}
	if _, exists := m.grants[key]; !exists && len(m.grants) >= maxGrants {
		return nil, tool.Denied("too many approval windows; try again after expiry")
	}
	now := m.now()
	expires := now.Add(m.window)
	m.grants[key] = expires
	return &audit.Event{
		Time: now, User: redact.String(user), Transport: transport, Tool: name,
		Outcome: "window_granted", Approval: method,
		Args: map[string]any{"window": windowLabel(m.window), "expires_at": expires.UTC().Format(time.RFC3339Nano)},
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
