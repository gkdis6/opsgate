package tool

import (
	"fmt"
	"path"
	"slices"

	"github.com/gkdis6/opsgate/internal/config"
)

// Policy filters registration and records all tools offered by enabled providers.
// Configure it before registration and call Validate after registration completes.
type Policy struct {
	allow, deny []string
	offered     []string
}

// NewPolicy snapshots the server-wide tool policy for one server's registration.
func NewPolicy(cfg config.ToolPolicy) *Policy {
	return &Policy{allow: slices.Clone(cfg.Allow), deny: slices.Clone(cfg.Deny)}
}

func (p *Policy) register(name string) bool {
	if p == nil {
		return true
	}
	// Record before filtering: a deny must also match tools excluded by Allow.
	p.offered = append(p.offered, name)
	return (len(p.allow) == 0 || matches(p.allow, name)) && !matches(p.deny, name)
}

// Validate rejects malformed patterns and patterns matching no offered tool.
func (p *Policy) Validate() error {
	if p == nil {
		return nil
	}
	for _, group := range []struct {
		name     string
		patterns []string
	}{{"allow", p.allow}, {"deny", p.deny}} {
		for _, pattern := range group.patterns {
			if _, err := path.Match(pattern, ""); err != nil {
				return fmt.Errorf("tools.%s pattern %q: invalid glob: %w", group.name, pattern, err)
			}
			if !slices.ContainsFunc(p.offered, func(name string) bool { return matches([]string{pattern}, name) }) {
				return fmt.Errorf("tools.%s pattern %q matches no tool", group.name, pattern)
			}
		}
	}
	return nil
}

func matches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
	}
	return false
}

func (c Caller) allowsTool(name string) bool {
	return len(c.Tools) == 0 || matches(c.Tools, name)
}
